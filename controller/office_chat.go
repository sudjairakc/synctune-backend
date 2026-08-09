// Package controller — meeting / DM channel chat handlers
package controller

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

// meetingSendPayload คือ Payload ของ event meeting_send
type meetingSendPayload struct {
	Text string `json:"text"`
}

// dmSendPayload คือ Payload ของ event dm_send
// to_connection_id ชี้ presence/connection เป้าหมาย; channel key ใช้ sorted User.ID
type dmSendPayload struct {
	ToConnectionID string `json:"to_connection_id"`
	Text           string `json:"text"`
}

// bubbleSendPayload คือ Payload ของ event bubble_send
type bubbleSendPayload struct {
	BubbleID string `json:"bubble_id"`
	Text     string `json:"text"`
}

// officeChatEventPayload คือ payload ของ meeting_message / dm_message
// (shape ใกล้ message_received + ฟิลด์ channel)
type officeChatEventPayload struct {
	Channel   string     `json:"channel"`
	ID        string     `json:"id"`
	User      model.User `json:"user"`
	Text      string     `json:"text"`
	Timestamp int64      `json:"timestamp"`
}

// dmChannelKey คืน channel key dm:{idA}:{idB} โดยเรียง User.ID
func dmChannelKey(userIDA, userIDB string) string {
	a, b := userIDA, userIDB
	if a > b {
		a, b = b, a
	}
	return "dm:" + a + ":" + b
}

// HandleMeetingSend จัดการ Event meeting_send — ใช้ zone จาก presence ฝั่ง server
func HandleMeetingSend(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if !client.ChatLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ส่งข้อความบ่อยเกินไป"})
		return
	}

	var payload meetingSendPayload
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

	m := office.DefaultMap()
	zoneID, zt := m.ZoneAt(client.LastX, client.LastY)
	if zt != office.ZoneMeeting || zoneID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_IN_MEETING", Message: "ต้องอยู่ใน meeting zone ก่อนส่งข้อความ"})
		return
	}

	channel := "meeting:" + zoneID
	msg := model.ChatMessage{
		ID:        uuid.New().String(),
		User:      client.User,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
	}

	ctx := context.Background()
	if err := h.Store().PushChannelMessage(ctx, client.RoomID, channel, msg); err != nil {
		log.Error().Err(err).Str("channel", channel).Msg("HandleMeetingSend: PushChannelMessage failed")
	}

	out := officeChatEventPayload{
		Channel:   channel,
		ID:        msg.ID,
		User:      msg.User,
		Text:      msg.Text,
		Timestamp: msg.Timestamp,
	}

	for _, c := range h.ClientsInRoom(client.RoomID) {
		if c.User.ID == "" {
			continue
		}
		cid, czt := m.ZoneAt(c.LastX, c.LastY)
		if czt != office.ZoneMeeting || cid != zoneID {
			continue
		}
		h.SendToClient(c.ID, "meeting_message", out)
	}

	log.Info().Str("event", "meeting_send").Str("user_id", client.User.ID).Str("channel", channel).Msg("meeting message sent")
}

// HandleDMSend จัดการ Event dm_send — ส่งเฉพาะคู่ connection (sender + target)
func HandleDMSend(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if !client.ChatLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ส่งข้อความบ่อยเกินไป"})
		return
	}

	var payload dmSendPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	toConnID := strings.TrimSpace(payload.ToConnectionID)
	text := strings.TrimSpace(payload.Text)
	if toConnID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "ต้องระบุ to_connection_id"})
		return
	}
	if text == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "EMPTY_MESSAGE", Message: "ข้อความว่าง"})
		return
	}
	if utf8.RuneCountInString(text) > maxMessageLen {
		text = string([]rune(text)[:maxMessageLen])
	}
	if toConnID == client.ID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_TARGET", Message: "ไม่สามารถ DM ตัวเองได้"})
		return
	}

	target := h.GetClient(toConnID)
	if target == nil || target.User.ID == "" || target.RoomID != client.RoomID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "TARGET_NOT_FOUND", Message: "ไม่พบผู้รับในห้องนี้"})
		return
	}

	// Channel key = sorted Client.User.ID (session ids; reconnect = new ids as today)
	channel := dmChannelKey(client.User.ID, target.User.ID)
	msg := model.ChatMessage{
		ID:        uuid.New().String(),
		User:      client.User,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
	}

	ctx := context.Background()
	if err := h.Store().PushChannelMessage(ctx, client.RoomID, channel, msg); err != nil {
		log.Error().Err(err).Str("channel", channel).Msg("HandleDMSend: PushChannelMessage failed")
	}

	out := officeChatEventPayload{
		Channel:   channel,
		ID:        msg.ID,
		User:      msg.User,
		Text:      msg.Text,
		Timestamp: msg.Timestamp,
	}

	h.SendToClient(client.ID, "dm_message", out)
	h.SendToClient(target.ID, "dm_message", out)

	log.Info().Str("event", "dm_send").Str("user_id", client.User.ID).Str("to", target.User.ID).Str("channel", channel).Msg("dm message sent")
}

// HandleBubbleSend จัดการ Event bubble_send — ส่งเฉพาะสมาชิก bubble (ไม่ broadcast ทั้งห้อง)
func HandleBubbleSend(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if !client.ChatLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ส่งข้อความบ่อยเกินไป"})
		return
	}

	var payload bubbleSendPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	bubbleID := strings.TrimSpace(payload.BubbleID)
	if bubbleID == "" {
		bubbleID = client.BubbleID
	}
	text := strings.TrimSpace(payload.Text)
	if bubbleID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_IN_BUBBLE", Message: "ต้องอยู่ใน bubble ก่อนส่งข้อความ"})
		return
	}
	if client.BubbleID != bubbleID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_IN_BUBBLE", Message: "คุณไม่ได้อยู่ใน bubble นี้"})
		return
	}
	if text == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "EMPTY_MESSAGE", Message: "ข้อความว่าง"})
		return
	}
	if utf8.RuneCountInString(text) > maxMessageLen {
		text = string([]rune(text)[:maxMessageLen])
	}

	ctx := context.Background()
	b, err := h.Store().GetBubble(ctx, client.RoomID, bubbleID)
	if err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("HandleBubbleSend: GetBubble failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถส่งข้อความได้"})
		return
	}
	if b == nil || !containsConn(b.Members, client.ID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_IN_BUBBLE", Message: "คุณไม่ได้อยู่ใน bubble นี้"})
		return
	}

	channel := "bubble:" + bubbleID
	msg := model.ChatMessage{
		ID:        uuid.New().String(),
		User:      client.User,
		Text:      text,
		Timestamp: time.Now().UnixMilli(),
	}

	if err := h.Store().PushChannelMessage(ctx, client.RoomID, channel, msg); err != nil {
		log.Error().Err(err).Str("channel", channel).Msg("HandleBubbleSend: PushChannelMessage failed")
	}

	out := officeChatEventPayload{
		Channel:   channel,
		ID:        msg.ID,
		User:      msg.User,
		Text:      msg.Text,
		Timestamp: msg.Timestamp,
	}

	for _, memberID := range b.Members {
		h.SendToClient(memberID, "bubble_message", out)
	}

	log.Info().Str("event", "bubble_send").Str("user_id", client.User.ID).Str("channel", channel).Msg("bubble message sent")
}
