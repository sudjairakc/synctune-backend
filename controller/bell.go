// Package controller — bell_ring handler
package controller

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/config"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

// bellRingPayload คือ Payload ของ event bell_ring จาก client
type bellRingPayload struct {
	TargetConnectionID string `json:"target_connection_id"`
}

// bellRingEventPayload คือ payload ที่ส่งถึงเป้าหมายเท่านั้น
type bellRingEventPayload struct {
	FromConnectionID string `json:"from_connection_id"`
	FromUserID       string `json:"from_user_id"`
	FromUsername     string `json:"from_username"`
}

// HandleBellRing จัดการ Event bell_ring — rate-limit ด้วย Redis TTL แล้วส่งถึงเป้าหมายเท่านั้น
func HandleBellRing(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload bellRingPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	targetID := strings.TrimSpace(payload.TargetConnectionID)
	if targetID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "ต้องระบุ target_connection_id"})
		return
	}
	if targetID == client.ID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_TARGET", Message: "ไม่สามารถ ring ตัวเองได้"})
		return
	}

	target := h.GetClient(targetID)
	if target == nil || target.User.ID == "" || target.RoomID != client.RoomID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "TARGET_NOT_FOUND", Message: "ไม่พบผู้รับในห้องนี้"})
		return
	}

	cfg := config.Load()
	ttl := time.Duration(cfg.BellCooldownMs) * time.Millisecond
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	ok, err := h.Store().TryClaimBell(context.Background(), client.RoomID, client.ID, targetID, ttl)
	if err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("HandleBellRing: TryClaimBell failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถ ring bell ได้"})
		return
	}
	if !ok {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", Message: "ring bell บ่อยเกินไป"})
		return
	}

	out := bellRingEventPayload{
		FromConnectionID: client.ID,
		FromUserID:       client.User.ID,
		FromUsername:     client.User.Username,
	}
	h.SendToClient(target.ID, "bell_ring", out)
	log.Info().Str("event", "bell_ring").Str("from", client.ID).Str("to", targetID).Msg("bell rung")
}
