// Package controller — chat handlers
package controller

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

const (
	maxUsernameLen = 30
	maxMessageLen  = 500
)

var roomIDRegex = regexp.MustCompile(`^\d{6}$`)

// joinPayload คือ Payload ของ event join
type joinPayload struct {
	Username   string `json:"username"`
	ProfileImg string `json:"profile_img"`
	RoomID     string `json:"room_id"`
}

// sendMessagePayload คือ Payload ของ event send_message
type sendMessagePayload struct {
	Text string `json:"text"`
}

// HandleJoin จัดการ Event join
// 1. Validate username → 2. Validate/Generate room_id
// 3. SetClientRoom + SetClientUser → 4. Send room_joined → 5. Broadcast user_joined
func HandleJoin(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	// ถ้า join แล้ว → ignore
	if client.User.ID != "" {
		return
	}

	var payload joinPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	username := strings.TrimSpace(payload.Username)
	if username == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_USERNAME", Message: "กรุณาระบุ username"})
		return
	}
	if utf8.RuneCountInString(username) > maxUsernameLen {
		username = string([]rune(username)[:maxUsernameLen])
	}

	// Room ID: ถ้าไม่ส่งมา → สร้างใหม่, ถ้าส่งมา → ตรวจ format
	roomID := strings.TrimSpace(payload.RoomID)
	if roomID == "" {
		roomID = generateRoomID()
	} else if !roomIDRegex.MatchString(roomID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_ROOM_ID", Message: "room_id ต้องเป็นตัวเลข 6 หลัก"})
		return
	}

	profileImg := strings.TrimSpace(payload.ProfileImg)

	user := model.User{
		ID:         client.ID,
		Username:   username,
		ProfileImg: profileImg,
	}

	// กำหนดห้องและ User ให้ Client
	h.SetClientRoom(client.ID, roomID)
	h.SetClientUser(client.ID, user)

	// โหลด state ของห้อง (สร้างใหม่ถ้าไม่มี)
	ctx := context.Background()
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("HandleJoin: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}
	history, err := h.Store().GetHistory(ctx, roomID)
	if err != nil {
		history = []model.HistorySong{}
	}
	chatHistory, err := h.Store().GetChatHistory(ctx, roomID)
	if err != nil {
		chatHistory = []model.ChatMessage{}
	}
	slices.Reverse(chatHistory)
	soundPad, err := h.Store().GetSoundPad(ctx, roomID)
	if err != nil {
		soundPad = make([]*model.SoundPadSlot, model.SoundPadSize)
	}
	soundPadHistory, err := h.Store().GetSoundPadHistory(ctx, roomID)
	if err != nil {
		soundPadHistory = []model.SoundPadPlayEvent{}
	}

	log.Info().Str("event", "join").Str("user_id", user.ID).Str("username", user.Username).Str("room_id", roomID).Msg("user joined room")

	// ส่ง room_joined เฉพาะ Client ที่ join
	broadcaster.SendRoomJoined(h, client.Conn, roomID, state, history, chatHistory, h.OnlineUsersInRoom(roomID), soundPad, soundPadHistory)

	// Broadcast user_joined ไปทุกคนในห้อง
	broadcaster.BroadcastUserJoined(h, roomID, user, h.OnlineUsersInRoom(roomID))
}

// HandleSendMessage จัดการ Event send_message
// 1. ตรวจว่า join แล้ว → 2. Rate limit → 3. Validate text
// 4. PushChatMessage → 5. Broadcast message_received
func HandleSendMessage(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if !client.ChatLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ส่งข้อความบ่อยเกินไป"})
		return
	}

	var payload sendMessagePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	text := strings.TrimSpace(payload.Text)
	if text == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "EMPTY_MESSAGE", Message: "ข้อความว่าง"})
		return
	}
	if utf8.RuneCountInString(text) > maxMessageLen {
		text = string([]rune(text)[:maxMessageLen])
	}

	msg := model.ChatMessage{
		ID:        uuid.New().String(),
		User:      client.User,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
	}

	ctx := context.Background()
	if err := h.Store().PushChatMessage(ctx, client.RoomID, msg); err != nil {
		log.Error().Err(err).Msg("HandleSendMessage: failed to push chat message")
	}

	log.Info().Str("event", "send_message").Str("user_id", msg.User.ID).Str("username", msg.User.Username).Str("room_id", client.RoomID).Msg("message sent")
	broadcaster.BroadcastMessageReceived(h, client.RoomID, msg)
}

// generateRoomID สร้าง room_id แบบสุ่ม 6 หลัก (100000–999999)
func generateRoomID() string {
	max := big.NewInt(900000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		// fallback ที่ไม่ควรเกิดขึ้น
		return fmt.Sprintf("%06d", time.Now().UnixNano()%900000+100000)
	}
	return fmt.Sprintf("%d", n.Int64()+100000)
}
