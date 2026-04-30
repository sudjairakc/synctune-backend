// Package hub จัดการ Pool ของ WebSocket Connections และ Route Events
package hub

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/olahol/melody"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
	"golang.org/x/time/rate"
)

const (
	addSongRatePerMinute     = 10
	reportErrorRatePerMinute = 5
	chatRatePerMinute        = 30
)

// Client แทน WebSocket Client แต่ละ Connection
type Client struct {
	ID                 string
	RoomID             string     // กำหนดหลัง join event
	User               model.User // กำหนดหลัง join event
	Conn               *melody.Session
	IP                 string
	AddSongLimiter     *rate.Limiter
	ReportErrorLimiter *rate.Limiter
	ChatLimiter        *rate.Limiter
}

// Hub จัดการ Connection Pool และ Broadcast
type Hub struct {
	clients        map[string]*Client              // clientID → Client
	rooms          map[string]map[string]*Client   // roomID → clientID → Client
	mu             sync.RWMutex
	store          store.Store
	broadcastCh    chan broadcastMsg
	messageHandler func(client *Client, msg model.WSMessage)
}

type broadcastMsg struct {
	roomID  string
	event   string
	payload interface{}
}

// NewHub สร้าง Hub ใหม่พร้อม Store ที่ใช้เก็บ PlaylistState
func NewHub(s store.Store) *Hub {
	return &Hub{
		clients:     make(map[string]*Client),
		rooms:       make(map[string]map[string]*Client),
		store:       s,
		broadcastCh: make(chan broadcastMsg, 256),
	}
}

// Store คืน Store ที่ Hub ใช้อยู่
func (h *Hub) Store() store.Store {
	return h.store
}

// SetMessageHandler กำหนด Function ที่จะรับ Event จาก Client
func (h *Hub) SetMessageHandler(fn func(client *Client, msg model.WSMessage)) {
	h.messageHandler = fn
}

// Run เริ่ม Broadcast Event Loop — เรียกใน Goroutine แยก
func (h *Hub) Run() {
	for msg := range h.broadcastCh {
		data, err := broadcaster.MarshalWSMessage(msg.event, msg.payload)
		if err != nil {
			log.Error().Err(err).Str("event", msg.event).Msg("failed to marshal broadcast message")
			continue
		}

		h.mu.RLock()
		roomClients := h.rooms[msg.roomID]
		clients := make([]*Client, 0, len(roomClients))
		for _, c := range roomClients {
			clients = append(clients, c)
		}
		h.mu.RUnlock()

		log.Debug().Str("event", msg.event).Str("room_id", msg.roomID).Int("clients", len(clients)).Msg("broadcasting to room")
		for _, c := range clients {
			if err := c.Conn.Write(data); err != nil {
				log.Warn().Str("client_id", c.ID).Err(err).Msg("failed to write to client")
			}
		}
	}
}

// Register เพิ่ม Client ใหม่เข้า Hub (ยังไม่อยู่ในห้อง — รอ join event)
func (h *Hub) Register(session *melody.Session) {
	clientID := uuid.New().String()
	client := &Client{
		ID:                 clientID,
		Conn:               session,
		IP:                 session.Request.RemoteAddr,
		AddSongLimiter:     rate.NewLimiter(rate.Limit(addSongRatePerMinute)/60.0, addSongRatePerMinute),
		ReportErrorLimiter: rate.NewLimiter(rate.Limit(reportErrorRatePerMinute)/60.0, reportErrorRatePerMinute),
		ChatLimiter:        rate.NewLimiter(rate.Limit(chatRatePerMinute)/60.0, chatRatePerMinute),
	}

	h.mu.Lock()
	h.clients[clientID] = client
	h.mu.Unlock()

	session.Set("client_id", clientID)
	log.Info().Str("client_id", clientID).Msg("client connected")
}

// Unregister ลบ Client ที่ Disconnect ออกจาก Hub
// ถ้าเป็น Client คนสุดท้ายในห้อง → ลบห้องออกจาก Redis ด้วย
func (h *Hub) Unregister(session *melody.Session) {
	clientID := h.sessionClientID(session)

	h.mu.Lock()
	client, ok := h.clients[clientID]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, clientID)

	roomEmpty := false
	roomID := client.RoomID
	if roomID != "" {
		if roomClients, exists := h.rooms[roomID]; exists {
			delete(roomClients, clientID)
			if len(roomClients) == 0 {
				delete(h.rooms, roomID)
				roomEmpty = true
			}
		}
	}
	h.mu.Unlock()

	log.Info().Str("client_id", clientID).Str("username", client.User.Username).Str("room_id", roomID).Msg("client disconnected")

	if roomEmpty {
		// ห้องว่าง → บันทึกเวลาไว้ให้ cleanup job ตรวจ (ไม่ลบทันที)
		if err := h.store.SetRoomLastEmptied(context.Background(), roomID); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Msg("failed to set room last_emptied")
		} else {
			log.Info().Str("room_id", roomID).Msg("room empty, marked for deferred cleanup")
		}
	} else if client.User.ID != "" && roomID != "" {
		// ยังมีคนอยู่ในห้อง → broadcast user_left
		broadcaster.BroadcastUserLeft(h, roomID, client.User, h.OnlineUsersInRoom(roomID))
	}
}

// SetClientRoom กำหนดห้องให้ Client และเพิ่ม Client เข้าใน rooms map
func (h *Hub) SetClientRoom(clientID, roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[clientID]
	if !ok {
		return
	}
	c.RoomID = roomID
	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[string]*Client)
	}
	h.rooms[roomID][clientID] = c
}

// SetClientUser กำหนด User ให้ Client หลัง join สำเร็จ
func (h *Hub) SetClientUser(clientID string, user model.User) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[clientID]; ok {
		c.User = user
	}
}

// OnlineUsersInRoom คืน slice ของ User ที่ join แล้วในห้องนั้น
func (h *Hub) OnlineUsersInRoom(roomID string) []model.User {
	h.mu.RLock()
	defer h.mu.RUnlock()
	roomClients := h.rooms[roomID]
	users := make([]model.User, 0, len(roomClients))
	for _, c := range roomClients {
		if c.User.ID != "" {
			users = append(users, c.User)
		}
	}
	return users
}

// SendToClient ส่ง Event ไปยัง Client ที่ระบุด้วย clientID
func (h *Hub) SendToClient(clientID, event string, payload interface{}) {
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	h.SendToSession(client.Conn, event, payload)
}

// ActiveRooms คืน slice ของ roomID ที่มี Client อยู่ในขณะนี้
func (h *Hub) ActiveRooms() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rooms := make([]string, 0, len(h.rooms))
	for roomID := range h.rooms {
		rooms = append(rooms, roomID)
	}
	return rooms
}

// HandleMessage Route WebSocket Event ไปยัง Controller ที่เหมาะสม
func (h *Hub) HandleMessage(session *melody.Session, msg []byte) {
	var wsMsg model.WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		h.SendToSession(session, "error", model.WSError{
			Code:    "INVALID_MESSAGE",
			Message: "รูปแบบ Message ไม่ถูกต้อง",
		})
		return
	}

	clientID := h.sessionClientID(session)
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	if h.messageHandler != nil {
		h.messageHandler(client, wsMsg)
	} else {
		log.Warn().Str("event", wsMsg.Event).Msg("no message handler registered")
	}
}

// BroadcastToRoom ส่ง Event ไปทุก Client ในห้องนั้น ผ่าน Channel
func (h *Hub) BroadcastToRoom(roomID, event string, payload interface{}) {
	h.mu.RLock()
	n := len(h.rooms[roomID])
	h.mu.RUnlock()
	log.Debug().Str("event", event).Str("room_id", roomID).Int("clients", n).Msg("broadcast queued")
	h.broadcastCh <- broadcastMsg{roomID: roomID, event: event, payload: payload}
}

// SendToSession ส่ง Event ไปยัง Client คนเดียว
func (h *Hub) SendToSession(session *melody.Session, event string, payload interface{}) {
	data, err := broadcaster.MarshalWSMessage(event, payload)
	if err != nil {
		log.Error().Err(err).Str("event", event).Msg("failed to marshal message")
		return
	}
	if err := session.Write(data); err != nil {
		log.Warn().Err(err).Msg("failed to send to session")
	}
}

// ClientCount คืนจำนวน Client ที่ Connect อยู่ในขณะนี้
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RoomCount คืนจำนวนห้องที่มีอยู่ในขณะนี้
func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

// RoomDetail แทนข้อมูลห้องสำหรับ admin
type RoomDetail struct {
	RoomID  string       `json:"room_id"`
	Users   []model.User `json:"users"`
}

// RoomsDetail คืนรายละเอียดทุกห้องสำหรับ admin
func (h *Hub) RoomsDetail() []RoomDetail {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rooms := make([]RoomDetail, 0, len(h.rooms))
	for roomID, clients := range h.rooms {
		users := make([]model.User, 0, len(clients))
		for _, c := range clients {
			if c.User.ID != "" {
				users = append(users, c.User)
			}
		}
		rooms = append(rooms, RoomDetail{RoomID: roomID, Users: users})
	}
	return rooms
}

// KickClient ตัด connection ของ client ออกจาก hub ด้วย clientID
// คืน false ถ้าไม่พบ client
func (h *Hub) KickClient(clientID string) bool {
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	client.Conn.Close()
	return true
}

// sessionClientID ดึง client_id จาก session (set ตอน Register)
func (h *Hub) sessionClientID(session *melody.Session) string {
	val, exists := session.Get("client_id")
	if !exists {
		return ""
	}
	id, _ := val.(string)
	return id
}
