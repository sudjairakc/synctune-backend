// Package controller — chat handlers
package controller

import (
	"context"
	"encoding/json"
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

// joinPayload คือ Payload ของ event join
type joinPayload struct {
	Username   string `json:"username"`
	ProfileImg string `json:"profile_img"`
}

// sendMessagePayload คือ Payload ของ event send_message
type sendMessagePayload struct {
	Text string `json:"text"`
}

// HandleJoin จัดการ Event join
// 1. Validate username → 2. สร้าง User → 3. Set ใน Hub → 4. Broadcast user_joined
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

	profileImg := strings.TrimSpace(payload.ProfileImg)

	user := model.User{
		ID:         client.ID,
		Username:   username,
		ProfileImg: profileImg,
	}

	h.SetClientUser(client.ID, user)

	log.Info().Str("event", "join").Str("user_id", user.ID).Str("username", user.Username).Msg("user joined")
	broadcaster.BroadcastUserJoined(h, user, h.OnlineUsers())
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
	if err := h.Store().PushChatMessage(ctx, msg); err != nil {
		log.Error().Err(err).Msg("HandleSendMessage: failed to push chat message")
	}

	log.Info().Str("event", "send_message").Str("user_id", msg.User.ID).Str("username", msg.User.Username).Msg("message sent")
	broadcaster.BroadcastMessageReceived(h, msg)
}
