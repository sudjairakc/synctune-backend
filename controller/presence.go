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
	ctx := context.Background()
	roomID := client.RoomID
	connID := client.ID

	m := office.DefaultMap()
	result := office.AcceptPresence(m, office.SanityInput{
		PrevX:       client.LastX,
		PrevY:       client.LastY,
		PrevTime:    client.LastPresenceAt,
		X:           payload.X,
		Y:           payload.Y,
		Now:         now,
		MaxSpeedPxS: float64(cfg.PresenceMaxSpeedPxPerSec),
		MinInterval: minInterval,
		CanEnterPrivate: func(zoneID string) bool {
			return canEnterPrivate(ctx, h.Store(), roomID, zoneID, connID)
		},
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
		BubbleID:     client.BubbleID,
		FollowingID:  client.FollowingID,
	}

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

	syncPrivateOccupancy(ctx, h.Store(), client.RoomID, client.ID, prevZone, result.ZoneID)

	broadcaster.BroadcastPresenceUpdate(h, client.RoomID, p)

	if result.Rejected {
		broadcaster.SendPresenceCorrected(h, client.Conn, result.X, result.Y, dir, result.ZoneID)
	}
	if result.ZoneID != prevZone {
		broadcaster.BroadcastZoneChanged(h, client.RoomID, client.ID, client.User.ID, result.ZoneID)
	}
	SyncActiveVoice(h, client)
}

// reconcileLivePresence returns presence for currently connected clients only.
// Stale Redis HASH fields (missed disconnect / crash / deploy) are deleted and
// presence_leave is broadcast so peers drop ghost avatars immediately.
func reconcileLivePresence(h *hub.Hub, roomID string) []model.Presence {
	ctx := context.Background()
	all, err := h.Store().GetAllPresence(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("reconcileLivePresence: GetAllPresence failed")
		return []model.Presence{}
	}
	live := make(map[string]struct{}, len(all))
	for _, c := range h.ClientsInRoom(roomID) {
		if c != nil && c.ID != "" {
			live[c.ID] = struct{}{}
		}
	}
	out := make([]model.Presence, 0, len(live))
	for _, p := range all {
		if _, ok := live[p.ConnectionID]; ok {
			out = append(out, p)
			continue
		}
		if err := h.Store().DeletePresence(ctx, roomID, p.ConnectionID); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Str("connection_id", p.ConnectionID).Msg("reconcileLivePresence: DeletePresence failed")
		}
		broadcaster.BroadcastPresenceLeave(h, roomID, p.ConnectionID, p.UserID)
		log.Info().Str("room_id", roomID).Str("connection_id", p.ConnectionID).Str("username", p.Username).Msg("purged stale presence ghost")
	}
	return out
}

// spawnPresence สร้าง Presence ที่จุด spawn และบันทึกลง store + client memory
func spawnPresence(h *hub.Hub, client *hub.Client, roomID string) (model.Presence, error) {
	return placePresence(h, client, roomID, office.SpawnX, office.SpawnY, "down")
}

// placePresence ตั้ง Presence ที่ (x,y) — ใช้ตอน spawn; ทดสอบ join+voice ใน meeting zone ได้
func placePresence(h *hub.Hub, client *hub.Client, roomID string, x, y float64, dir string) (model.Presence, error) {
	m := office.DefaultMap()
	zoneID, _ := m.ZoneAt(x, y)
	now := time.Now()
	if dir == "" {
		dir = "down"
	}

	p := model.Presence{
		ConnectionID: client.ID,
		UserID:       client.User.ID,
		Username:     client.User.Username,
		ProfileImg:   client.User.ProfileImg,
		X:            x,
		Y:            y,
		Dir:          dir,
		ZoneID:       zoneID,
		BubbleID:     client.BubbleID,
		FollowingID:  client.FollowingID,
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
