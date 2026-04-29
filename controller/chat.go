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
	maxEmojiLen    = 10
	maxImageData   = 1 << 20 // 1 MB base64
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
	Text      string `json:"text"`
	ReplyToID string `json:"reply_to_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	ImageData string `json:"image_data,omitempty"` // base64 data URI (data:image/...)
}

// deleteMessagePayload คือ Payload ของ event delete_message
type deleteMessagePayload struct {
	MessageID string `json:"message_id"`
}

// reactMessagePayload คือ Payload ของ event react_message
type reactMessagePayload struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// pinMessagePayload คือ Payload ของ event pin_message
type pinMessagePayload struct {
	MessageID string `json:"message_id"`
}

// HandleJoin จัดการ Event join
func HandleJoin(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
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

	h.SetClientRoom(client.ID, roomID)
	h.SetClientUser(client.ID, user)

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

	broadcaster.SendRoomJoined(h, client.Conn, roomID, state, history, chatHistory, h.OnlineUsersInRoom(roomID), soundPad, soundPadHistory)
	broadcaster.BroadcastUserJoined(h, roomID, user, h.OnlineUsersInRoom(roomID))

	// ส่ง pins ปัจจุบันให้ client ที่ join
	pins, err := h.Store().GetPinnedMessages(ctx, roomID)
	if err == nil && len(pins) > 0 {
		broadcaster.BroadcastPinsUpdated(h, roomID, pins)
	}
}

// HandleSendMessage จัดการ Event send_message (รองรับ reply, thread, image)
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
	imageData := strings.TrimSpace(payload.ImageData)

	if text == "" && imageData == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "EMPTY_MESSAGE", Message: "ข้อความว่าง"})
		return
	}
	if utf8.RuneCountInString(text) > maxMessageLen {
		text = string([]rune(text)[:maxMessageLen])
	}
	if imageData != "" {
		if !strings.HasPrefix(imageData, "data:image/") {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_IMAGE", Message: "รูปแบบรูปภาพไม่ถูกต้อง"})
			return
		}
		if len(imageData) > maxImageData {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "IMAGE_TOO_LARGE", Message: "รูปภาพใหญ่เกินไป (max 1MB)"})
			return
		}
	}

	msg := model.ChatMessage{
		ID:        uuid.New().String(),
		User:      client.User,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
		ImageURL:  imageData,
		ThreadID:  strings.TrimSpace(payload.ThreadID),
	}

	// ถ้ามี reply_to_id ให้ค้นหาข้อความต้นทาง
	if replyToID := strings.TrimSpace(payload.ReplyToID); replyToID != "" {
		ctx := context.Background()
		original, err := h.Store().GetChatMessageByID(ctx, client.RoomID, replyToID)
		if err == nil && original != nil && !original.Deleted {
			preview := original.Text
			if utf8.RuneCountInString(preview) > 100 {
				preview = string([]rune(preview)[:100]) + "…"
			}
			ref := &model.ChatMessageRef{
				ID:       original.ID,
				Username: original.User.Username,
				Text:     preview,
			}
			if original.ImageURL != "" {
				ref.ImageURL = "[image]"
			}
			msg.ReplyTo = ref
		}
	}

	ctx := context.Background()
	if err := h.Store().PushChatMessage(ctx, client.RoomID, msg); err != nil {
		log.Error().Err(err).Msg("HandleSendMessage: failed to push chat message")
	}

	log.Info().Str("event", "send_message").Str("user_id", msg.User.ID).Str("room_id", client.RoomID).Msg("message sent")
	broadcaster.BroadcastMessageReceived(h, client.RoomID, msg)
}

// HandleDeleteMessage จัดการ Event delete_message (เฉพาะเจ้าของข้อความ)
func HandleDeleteMessage(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload deleteMessagePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	msgID := strings.TrimSpace(payload.MessageID)
	if msgID == "" {
		return
	}

	ctx := context.Background()
	// ตรวจสอบว่าเป็นเจ้าของข้อความ
	original, err := h.Store().GetChatMessageByID(ctx, client.RoomID, msgID)
	if err != nil || original == nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_FOUND", Message: "ไม่พบข้อความ"})
		return
	}
	if original.User.ID != client.User.ID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "FORBIDDEN", Message: "ลบได้เฉพาะข้อความของตัวเอง"})
		return
	}

	if err := h.Store().DeleteChatMessage(ctx, client.RoomID, msgID); err != nil {
		log.Error().Err(err).Msg("HandleDeleteMessage: failed to delete")
		return
	}

	log.Info().Str("event", "delete_message").Str("user_id", client.User.ID).Str("msg_id", msgID).Msg("message deleted")
	broadcaster.BroadcastMessageDeleted(h, client.RoomID, msgID)
}

// HandleReactMessage จัดการ Event react_message (toggle emoji reaction)
func HandleReactMessage(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if !client.ChatLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ส่งบ่อยเกินไป"})
		return
	}

	var payload reactMessagePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	msgID := strings.TrimSpace(payload.MessageID)
	emoji := strings.TrimSpace(payload.Emoji)
	if msgID == "" || emoji == "" {
		return
	}
	if utf8.RuneCountInString(emoji) > maxEmojiLen {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_EMOJI", Message: "emoji ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	reactions, err := h.Store().ToggleChatReaction(ctx, client.RoomID, msgID, emoji, client.User.ID)
	if err != nil {
		log.Error().Err(err).Msg("HandleReactMessage: failed to toggle reaction")
		return
	}

	broadcaster.BroadcastMessageReacted(h, client.RoomID, msgID, reactions)
}

// HandlePinMessage จัดการ Event pin_message (toggle pin)
func HandlePinMessage(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload pinMessagePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	msgID := strings.TrimSpace(payload.MessageID)
	if msgID == "" {
		return
	}

	ctx := context.Background()
	// ดึงข้อความที่จะ pin
	original, err := h.Store().GetChatMessageByID(ctx, client.RoomID, msgID)
	if err != nil || original == nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_FOUND", Message: "ไม่พบข้อความ"})
		return
	}
	if original.Deleted {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "DELETED", Message: "ข้อความถูกลบแล้ว"})
		return
	}

	_, pins, err := h.Store().TogglePinMessage(ctx, client.RoomID, *original)
	if err != nil {
		log.Error().Err(err).Msg("HandlePinMessage: failed to toggle pin")
		return
	}

	broadcaster.BroadcastPinsUpdated(h, client.RoomID, pins)
}

// generateRoomID สร้าง room_id แบบสุ่ม 6 หลัก (100000–999999)
func generateRoomID() string {
	max := big.NewInt(900000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%900000+100000)
	}
	return fmt.Sprintf("%d", n.Int64()+100000)
}
