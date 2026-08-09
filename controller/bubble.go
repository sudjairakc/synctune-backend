// Package controller — bubble invite / accept / leave (ownerless membership)
package controller

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

// bubbleInvitePayload คือ Payload ของ event bubble_invite จาก client
type bubbleInvitePayload struct {
	ToConnectionID string `json:"to_connection_id"`
}

// bubbleAcceptPayload คือ Payload ของ event bubble_accept จาก client
type bubbleAcceptPayload struct {
	BubbleID string `json:"bubble_id"`
}

// bubbleInviteEventPayload ส่งถึงผู้ถูกเชิญ
type bubbleInviteEventPayload struct {
	BubbleID         string `json:"bubble_id"`
	FromConnectionID string `json:"from_connection_id"`
	FromUserID       string `json:"from_user_id"`
	FromUsername     string `json:"from_username"`
}

func persistBubblePresence(h *hub.Hub, client *hub.Client) {
	ctx := context.Background()
	p := presenceFromClient(client)
	if err := h.Store().SetPresence(ctx, client.RoomID, p); err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("persistBubblePresence: SetPresence failed")
		return
	}
	broadcaster.BroadcastPresenceUpdate(h, client.RoomID, p)
}

func broadcastBubble(h *hub.Hub, roomID string, b *model.Bubble) {
	members := []string{}
	if b != nil && b.Members != nil {
		members = b.Members
	}
	id := ""
	if b != nil {
		id = b.ID
	}
	broadcaster.BroadcastBubbleUpdated(h, roomID, id, members)
}

// HandleBubbleInvite จัดการ Event bubble_invite
// ถ้า inviter อยู่ใน bubble แล้ว → เพิ่ม invite ใน bubble นั้น
// ถ้ายังไม่อยู่ → สร้าง bubble ใหม่ (inviter เป็นสมาชิกทันที + pending invite)
func HandleBubbleInvite(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload bubbleInvitePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	toID := strings.TrimSpace(payload.ToConnectionID)
	if toID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "ต้องระบุ to_connection_id"})
		return
	}
	if toID == client.ID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_TARGET", Message: "ไม่สามารถเชิญตัวเองได้"})
		return
	}

	target := h.GetClient(toID)
	if target == nil || target.User.ID == "" || target.RoomID != client.RoomID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "TARGET_NOT_FOUND", Message: "ไม่พบผู้รับในห้องนี้"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID

	b, err := h.Store().FindBubbleByMember(ctx, roomID, client.ID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("HandleBubbleInvite: FindBubbleByMember failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเชิญได้"})
		return
	}

	created := false
	if b == nil {
		b = &model.Bubble{
			ID:      uuid.New().String(),
			Members: []string{client.ID},
			Invites: []string{},
		}
		created = true
	}

	if containsConn(b.Members, toID) {
		return // already a member — no-op
	}
	if !containsConn(b.Invites, toID) {
		b.Invites = append(b.Invites, toID)
	}

	if err := h.Store().SetBubble(ctx, roomID, b); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("HandleBubbleInvite: SetBubble failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเชิญได้"})
		return
	}

	if created {
		client.BubbleID = b.ID
		persistBubblePresence(h, client)
		broadcastBubble(h, roomID, b)
		SyncActiveVoice(h, client)
	}

	h.SendToClient(target.ID, "bubble_invite", bubbleInviteEventPayload{
		BubbleID:         b.ID,
		FromConnectionID: client.ID,
		FromUserID:       client.User.ID,
		FromUsername:     client.User.Username,
	})
	log.Info().Str("event", "bubble_invite").Str("bubble_id", b.ID).Str("from", client.ID).Str("to", toID).Msg("bubble invite sent")
}

// HandleBubbleAccept จัดการ Event bubble_accept — ต้องมี invite ค้างอยู่
func HandleBubbleAccept(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload bubbleAcceptPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	bubbleID := strings.TrimSpace(payload.BubbleID)
	if bubbleID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "ต้องระบุ bubble_id"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID

	b, err := h.Store().GetBubble(ctx, roomID, bubbleID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("HandleBubbleAccept: GetBubble failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเข้าร่วม bubble ได้"})
		return
	}
	if b == nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "BUBBLE_NOT_FOUND", Message: "ไม่พบ bubble"})
		return
	}
	if containsConn(b.Members, client.ID) {
		return // already member — no-op
	}
	if !containsConn(b.Invites, client.ID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_INVITED", Message: "ไม่มีคำเชิญสำหรับ bubble นี้"})
		return
	}

	// ถ้าอยู่ bubble อื่นอยู่แล้ว → ออกก่อน
	if client.BubbleID != "" && client.BubbleID != bubbleID {
		leaveBubbleMembership(h, client)
		b, err = h.Store().GetBubble(ctx, roomID, bubbleID)
		if err != nil || b == nil {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "BUBBLE_NOT_FOUND", Message: "ไม่พบ bubble"})
			return
		}
	}

	b.Members = append(b.Members, client.ID)
	b.Invites = removeConn(b.Invites, client.ID)
	if err := h.Store().SetBubble(ctx, roomID, b); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("HandleBubbleAccept: SetBubble failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเข้าร่วม bubble ได้"})
		return
	}

	client.BubbleID = bubbleID
	persistBubblePresence(h, client)
	broadcastBubble(h, roomID, b)
	SyncActiveVoice(h, client)
	log.Info().Str("event", "bubble_accept").Str("bubble_id", bubbleID).Str("connection_id", client.ID).Msg("bubble accepted")
}

// HandleBubbleLeave จัดการ Event bubble_leave — ออกจาก bubble ปัจจุบัน
func HandleBubbleLeave(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	if client.BubbleID == "" {
		return
	}
	leaveBubbleMembership(h, client)
}

// leaveBubbleMembership ลบ client ออกจาก bubble ปัจจุบัน; ว่าง → ลบ bubble
func leaveBubbleMembership(h *hub.Hub, client *hub.Client) {
	bubbleID := client.BubbleID
	if bubbleID == "" {
		return
	}
	ctx := context.Background()
	roomID := client.RoomID

	b, err := h.Store().GetBubble(ctx, roomID, bubbleID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("leaveBubbleMembership: GetBubble failed")
		return
	}

	client.BubbleID = ""
	persistBubblePresence(h, client)
	SyncActiveVoice(h, client)

	if b == nil {
		return
	}

	b.Members = removeConn(b.Members, client.ID)
	b.Invites = removeConn(b.Invites, client.ID)

	if len(b.Members) == 0 {
		if err := h.Store().DeleteBubble(ctx, roomID, bubbleID); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Str("bubble_id", bubbleID).Msg("leaveBubbleMembership: DeleteBubble failed")
		}
		broadcastBubble(h, roomID, &model.Bubble{ID: bubbleID, Members: []string{}})
		log.Info().Str("event", "bubble_leave").Str("bubble_id", bubbleID).Str("connection_id", client.ID).Msg("bubble torn down")
		return
	}

	if err := h.Store().SetBubble(ctx, roomID, b); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("leaveBubbleMembership: SetBubble failed")
		return
	}
	broadcastBubble(h, roomID, b)
	log.Info().Str("event", "bubble_leave").Str("bubble_id", bubbleID).Str("connection_id", client.ID).Msg("left bubble")
}
