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
	User               model.User // กำหนดหลัง join event
	Conn               *melody.Session
	IP                 string
	AddSongLimiter     *rate.Limiter
	ReportErrorLimiter *rate.Limiter
	ChatLimiter        *rate.Limiter
}

// Hub จัดการ Connection Pool และ Broadcast
type Hub struct {
	clients        map[string]*Client
	mu             sync.RWMutex
	store          store.Store
	broadcastCh    chan broadcastMsg
	messageHandler func(client *Client, msg model.WSMessage)
}

type broadcastMsg struct {
	event   string
	payload interface{}
}

// NewHub สร้าง Hub ใหม่พร้อม Store ที่ใช้เก็บ PlaylistState
// ต้องเรียก Run() แยกต่างหากเพื่อเริ่ม Event Loop Goroutine
func NewHub(s store.Store) *Hub {
	return &Hub{
		clients:     make(map[string]*Client),
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

		// Copy clients นอก Lock ก่อน Iterate เพื่อไม่ให้ Block Goroutine อื่น
		h.mu.RLock()
		clients := make([]*Client, 0, len(h.clients))
		for _, c := range h.clients {
			clients = append(clients, c)
		}
		h.mu.RUnlock()

		log.Debug().Str("event", msg.event).Int("clients", len(clients)).Msg("broadcasting to clients")
		for _, c := range clients {
			if err := c.Conn.Write(data); err != nil {
				log.Warn().Str("client_id", c.ID).Err(err).Msg("failed to write to client")
			}
		}
	}
}

// Register เพิ่ม Client ใหม่เข้า Hub และส่ง initial_state
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

	// เก็บ clientID ไว้ใน session เพื่อดึงกลับตอน HandleMessage / Unregister
	session.Set("client_id", clientID)

	log.Info().Str("client_id", clientID).Msg("client connected")

	// ส่ง initial_state ให้ Client ใหม่
	ctx := context.Background()
	state, err := h.store.GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to get state for initial_state")
		return
	}
	history, err := h.store.GetHistory(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to get history for initial_state")
		history = []model.HistorySong{}
	}
	chatHistory, err := h.store.GetChatHistory(ctx)
	if err != nil {
		log.Error().Err(err).Msg("failed to get chat history for initial_state")
		chatHistory = []model.ChatMessage{}
	}
	broadcaster.SendInitialState(h, session, state, history, chatHistory, h.OnlineUsers())
}

// Unregister ลบ Client ที่ Disconnect ออกจาก Hub และ broadcast user_left
func (h *Hub) Unregister(session *melody.Session) {
	clientID := h.sessionClientID(session)

	h.mu.Lock()
	client, ok := h.clients[clientID]
	if ok {
		delete(h.clients, clientID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	log.Info().Str("client_id", clientID).Str("username", client.User.Username).Msg("client disconnected")

	// broadcast user_left เฉพาะถ้า join แล้ว
	if client.User.ID != "" {
		broadcaster.BroadcastUserLeft(h, client.User, h.OnlineUsers())
	}
}

// SetClientUser กำหนด User ให้ Client หลัง join สำเร็จ
func (h *Hub) SetClientUser(clientID string, user model.User) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[clientID]; ok {
		c.User = user
	}
}

// OnlineUsers คืน slice ของ User ที่ join แล้วทุกคน
func (h *Hub) OnlineUsers() []model.User {
	h.mu.RLock()
	defer h.mu.RUnlock()
	users := make([]model.User, 0, len(h.clients))
	for _, c := range h.clients {
		if c.User.ID != "" {
			users = append(users, c.User)
		}
	}
	return users
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

// Broadcast ส่ง Event ไปทุก Client ผ่าน Channel
func (h *Hub) Broadcast(event string, payload interface{}) {
	h.mu.RLock()
	n := len(h.clients)
	h.mu.RUnlock()
	log.Debug().Str("event", event).Int("clients", n).Msg("broadcast queued")
	h.broadcastCh <- broadcastMsg{event: event, payload: payload}
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

// sessionClientID ดึง client_id จาก session (set ตอน Register)
func (h *Hub) sessionClientID(session *melody.Session) string {
	val, exists := session.Get("client_id")
	if !exists {
		return ""
	}
	id, _ := val.(string)
	return id
}
