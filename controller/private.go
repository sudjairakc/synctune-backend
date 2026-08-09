// Package controller — private zone invite + access enforcement
package controller

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
	"github.com/synctune/backend/store"
)

// privateInvitePayload คือ Payload ของ event private_invite จาก client
type privateInvitePayload struct {
	ZoneID         string `json:"zone_id"`
	ToConnectionID string `json:"to_connection_id"`
}

// privateInviteEventPayload ส่งถึงผู้ถูกเชิญ
type privateInviteEventPayload struct {
	ZoneID           string `json:"zone_id"`
	FromConnectionID string `json:"from_connection_id"`
	FromUserID       string `json:"from_user_id"`
	FromUsername     string `json:"from_username"`
}

// canEnterPrivate ตรวจว่า connection เข้า private zone ได้หรือไม่
// empty → first entrant เข้าได้; occupied → ต้องเป็น occupant หรือ invitee
func canEnterPrivate(ctx context.Context, s store.Store, roomID, zoneID, connectionID string) bool {
	st, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("canEnterPrivate: GetPrivateZoneState failed")
		return false
	}
	if len(st.Occupants) == 0 {
		return true
	}
	if containsConn(st.Occupants, connectionID) {
		return true
	}
	return containsConn(st.Invites, connectionID)
}

// syncPrivateOccupancy อัปเดต occupants เมื่อ zone เปลี่ยน (derived leave/enter)
// ออกจาก private → ลบ occupant; ว่าง → เคลียร์ invites
// เข้า private → เพิ่ม occupant และลบออกจาก invites
func syncPrivateOccupancy(ctx context.Context, s store.Store, roomID, connectionID, prevZoneID, newZoneID string) {
	m := office.DefaultMap()

	if prevZoneID != newZoneID && m.IsPrivateZone(prevZoneID) {
		removePrivateOccupant(ctx, s, roomID, prevZoneID, connectionID)
	}
	if m.IsPrivateZone(newZoneID) {
		addPrivateOccupant(ctx, s, roomID, newZoneID, connectionID)
	}
}

func addPrivateOccupant(ctx context.Context, s store.Store, roomID, zoneID, connectionID string) {
	st, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("addPrivateOccupant: get failed")
		return
	}
	if !containsConn(st.Occupants, connectionID) {
		st.Occupants = append(st.Occupants, connectionID)
	}
	st.Invites = removeConn(st.Invites, connectionID)
	if err := s.SetPrivateZoneState(ctx, roomID, zoneID, st); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("addPrivateOccupant: set failed")
	}
}

func removePrivateOccupant(ctx context.Context, s store.Store, roomID, zoneID, connectionID string) {
	st, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("removePrivateOccupant: get failed")
		return
	}
	st.Occupants = removeConn(st.Occupants, connectionID)
	if len(st.Occupants) == 0 {
		// clear invites when empty; drop key entirely
		if err := s.DeletePrivateZoneState(ctx, roomID, zoneID); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("removePrivateOccupant: delete failed")
		}
		return
	}
	st.Invites = removeConn(st.Invites, connectionID)
	if err := s.SetPrivateZoneState(ctx, roomID, zoneID, st); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Str("zone_id", zoneID).Msg("removePrivateOccupant: set failed")
	}
}

// HandlePrivateInvite จัดการ Event private_invite — เฉพาะ occupant ปัจจุบันเท่านั้น
func HandlePrivateInvite(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload privateInvitePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	zoneID := strings.TrimSpace(payload.ZoneID)
	toID := strings.TrimSpace(payload.ToConnectionID)
	if zoneID == "" || toID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "ต้องระบุ zone_id และ to_connection_id"})
		return
	}
	if toID == client.ID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_TARGET", Message: "ไม่สามารถเชิญตัวเองได้"})
		return
	}

	m := office.DefaultMap()
	if !m.IsPrivateZone(zoneID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_ZONE", Message: "zone นี้ไม่ใช่ private zone"})
		return
	}

	target := h.GetClient(toID)
	if target == nil || target.User.ID == "" || target.RoomID != client.RoomID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "TARGET_NOT_FOUND", Message: "ไม่พบผู้รับในห้องนี้"})
		return
	}

	ctx := context.Background()
	st, err := h.Store().GetPrivateZoneState(ctx, client.RoomID, zoneID)
	if err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("HandlePrivateInvite: GetPrivateZoneState failed")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเชิญได้"})
		return
	}
	if !containsConn(st.Occupants, client.ID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_OCCUPANT", Message: "ต้องอยู่ภายใน private zone ก่อนจึงจะเชิญได้"})
		return
	}
	if containsConn(st.Occupants, toID) {
		return // already inside — no-op
	}
	if !containsConn(st.Invites, toID) {
		st.Invites = append(st.Invites, toID)
		if err := h.Store().SetPrivateZoneState(ctx, client.RoomID, zoneID, st); err != nil {
			log.Error().Err(err).Str("room_id", client.RoomID).Msg("HandlePrivateInvite: SetPrivateZoneState failed")
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INTERNAL_ERROR", Message: "ไม่สามารถเชิญได้"})
			return
		}
	}

	h.SendToClient(target.ID, "private_invite", privateInviteEventPayload{
		ZoneID:           zoneID,
		FromConnectionID: client.ID,
		FromUserID:       client.User.ID,
		FromUsername:     client.User.Username,
	})
	log.Info().Str("event", "private_invite").Str("zone_id", zoneID).Str("from", client.ID).Str("to", toID).Msg("private invite sent")
}

func containsConn(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func removeConn(ids []string, drop string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != drop {
			out = append(out, id)
		}
	}
	return out
}
