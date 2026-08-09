// Package controller — follow_start / follow_stop handlers
package controller

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

// followStartPayload คือ Payload ของ event follow_start
type followStartPayload struct {
	TargetConnectionID string `json:"target_connection_id"`
}

func presenceFromClient(client *hub.Client) model.Presence {
	return model.Presence{
		ConnectionID: client.ID,
		UserID:       client.User.ID,
		Username:     client.User.Username,
		ProfileImg:   client.User.ProfileImg,
		X:            client.LastX,
		Y:            client.LastY,
		Dir:          client.LastDir,
		ZoneID:       client.LastZoneID,
		BubbleID:     client.BubbleID,
		FollowingID:  client.FollowingID,
	}
}

func persistFollowing(h *hub.Hub, client *hub.Client) {
	ctx := context.Background()
	p := presenceFromClient(client)
	if err := h.Store().SetPresence(ctx, client.RoomID, p); err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("persistFollowing: SetPresence failed")
	}
	broadcaster.BroadcastFollowUpdated(h, client.RoomID, client.ID, client.FollowingID)
}

// HandleFollowStart จัดการ Event follow_start — ตั้ง following_id แล้ว broadcast follow_updated
func HandleFollowStart(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload followStartPayload
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
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_TARGET", Message: "ไม่สามารถ follow ตัวเองได้"})
		return
	}

	target := h.GetClient(targetID)
	if target == nil || target.User.ID == "" || target.RoomID != client.RoomID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "TARGET_NOT_FOUND", Message: "ไม่พบเป้าหมายในห้องนี้"})
		return
	}

	client.FollowingID = targetID
	persistFollowing(h, client)
	log.Info().Str("event", "follow_start").Str("user_id", client.User.ID).Str("target", targetID).Msg("follow started")
}

// HandleFollowStop จัดการ Event follow_stop — ล้าง following_id แล้ว broadcast follow_updated
func HandleFollowStop(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	if client.FollowingID == "" {
		return
	}

	client.FollowingID = ""
	persistFollowing(h, client)
	log.Info().Str("event", "follow_stop").Str("user_id", client.User.ID).Msg("follow stopped")
}
