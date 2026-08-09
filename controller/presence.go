package controller

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/config"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

// presenceUpdatePayload คือ Payload ของ event presence_update จาก client
type presenceUpdatePayload struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Dir string  `json:"dir"`
}

var validDirs = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
}

// canEnterPrivatePhase1 — Phase 1 stub: deny all private zones
func canEnterPrivatePhase1(string) bool { return false }

// HandlePresenceUpdate จัดการ Event presence_update
func HandlePresenceUpdate(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	var payload presenceUpdatePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	dir := payload.Dir
	if !validDirs[dir] {
		if client.LastDir != "" {
			dir = client.LastDir
		} else {
			dir = "down"
		}
	}

	cfg := config.Load()
	now := time.Now()
	minInterval := time.Duration(cfg.PresenceMinIntervalMs) * time.Millisecond

	m := office.DefaultMap()
	result := office.AcceptPresence(m, office.SanityInput{
		PrevX:           client.LastX,
		PrevY:           client.LastY,
		PrevTime:        client.LastPresenceAt,
		X:               payload.X,
		Y:               payload.Y,
		Now:             now,
		MaxSpeedPxS:     float64(cfg.PresenceMaxSpeedPxPerSec),
		MinInterval:     minInterval,
		CanEnterPrivate: canEnterPrivatePhase1,
	})

	// Interval-too-soon ignore: Rejected=false + position unchanged → skip broadcast/persist
	if !result.Rejected && result.X == client.LastX && result.Y == client.LastY &&
		now.Sub(client.LastPresenceAt) < minInterval {
		return
	}

	p := model.Presence{
		ConnectionID: client.ID,
		UserID:       client.User.ID,
		Username:     client.User.Username,
		ProfileImg:   client.User.ProfileImg,
		X:            result.X,
		Y:            result.Y,
		Dir:          dir,
		ZoneID:       result.ZoneID,
	}

	ctx := context.Background()
	if err := h.Store().SetPresence(ctx, client.RoomID, p); err != nil {
		log.Error().Err(err).Str("room_id", client.RoomID).Msg("HandlePresenceUpdate: SetPresence failed")
		return
	}

	prevZone := client.LastZoneID
	client.LastX = result.X
	client.LastY = result.Y
	client.LastZoneID = result.ZoneID
	client.LastDir = dir
	client.LastPresenceAt = now

	broadcaster.BroadcastPresenceUpdate(h, client.RoomID, p)

	if result.Rejected {
		broadcaster.SendPresenceCorrected(h, client.Conn, result.X, result.Y, dir, result.ZoneID)
	}
	if result.ZoneID != prevZone {
		broadcaster.BroadcastZoneChanged(h, client.RoomID, client.ID, client.User.ID, result.ZoneID)
	}
}

// spawnPresence สร้าง Presence ที่จุด spawn และบันทึกลง store + client memory
func spawnPresence(h *hub.Hub, client *hub.Client, roomID string) (model.Presence, error) {
	m := office.DefaultMap()
	zoneID, _ := m.ZoneAt(office.SpawnX, office.SpawnY)
	now := time.Now()

	p := model.Presence{
		ConnectionID: client.ID,
		UserID:       client.User.ID,
		Username:     client.User.Username,
		ProfileImg:   client.User.ProfileImg,
		X:            office.SpawnX,
		Y:            office.SpawnY,
		Dir:          "down",
		ZoneID:       zoneID,
	}

	if err := h.Store().SetPresence(context.Background(), roomID, p); err != nil {
		return p, err
	}

	client.LastX = p.X
	client.LastY = p.Y
	client.LastZoneID = p.ZoneID
	client.LastDir = p.Dir
	client.LastPresenceAt = now
	return p, nil
}
