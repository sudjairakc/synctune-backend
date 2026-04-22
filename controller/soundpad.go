// Package controller — sound pad handlers
package controller

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

const maxSoundPadTitleLen = 100

type soundPadSetPayload struct {
	Slot    int    `json:"slot"`
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
}

type soundPadClearPayload struct {
	Slot int `json:"slot"`
}

type soundPadPlayPayload struct {
	Slot int `json:"slot"`
}

// HandleSoundPadSet เพิ่ม/แก้ไข slot — validate → save Redis → broadcast soundpad_updated
func HandleSoundPadSet(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	var payload soundPadSetPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return
	}
	if payload.Slot < 0 || payload.Slot >= model.SoundPadSize {
		return
	}
	if len(payload.VideoID) != 11 {
		return
	}
	if utf8.RuneCountInString(payload.Title) > maxSoundPadTitleLen {
		payload.Title = string([]rune(payload.Title)[:maxSoundPadTitleLen])
	}

	ctx := context.Background()
	pad, err := h.Store().GetSoundPad(ctx, client.RoomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSoundPadSet: failed to get pad")
		return
	}
	pad[payload.Slot] = &model.SoundPadSlot{VideoID: payload.VideoID, Title: payload.Title}
	if err := h.Store().SetSoundPad(ctx, client.RoomID, pad); err != nil {
		log.Error().Err(err).Msg("HandleSoundPadSet: failed to save pad")
		return
	}
	log.Info().Str("room_id", client.RoomID).Int("slot", payload.Slot).Str("video_id", payload.VideoID).Msg("soundpad slot set")
	broadcaster.BroadcastSoundPadUpdated(h, client.RoomID, pad)
}

// HandleSoundPadClear ลบ slot — save Redis → broadcast soundpad_updated
func HandleSoundPadClear(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	var payload soundPadClearPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return
	}
	if payload.Slot < 0 || payload.Slot >= model.SoundPadSize {
		return
	}

	ctx := context.Background()
	pad, err := h.Store().GetSoundPad(ctx, client.RoomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSoundPadClear: failed to get pad")
		return
	}
	pad[payload.Slot] = nil
	if err := h.Store().SetSoundPad(ctx, client.RoomID, pad); err != nil {
		log.Error().Err(err).Msg("HandleSoundPadClear: failed to save pad")
		return
	}
	log.Info().Str("room_id", client.RoomID).Int("slot", payload.Slot).Msg("soundpad slot cleared")
	broadcaster.BroadcastSoundPadUpdated(h, client.RoomID, pad)
}

// HandleSoundPadPlay broadcast trigger เล่นเสียง — ไม่แตะ Redis
func HandleSoundPadPlay(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	var payload soundPadPlayPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return
	}
	if payload.Slot < 0 || payload.Slot >= model.SoundPadSize {
		return
	}

	ctx := context.Background()
	pad, err := h.Store().GetSoundPad(ctx, client.RoomID)
	if err != nil || pad[payload.Slot] == nil {
		return
	}
	videoID := pad[payload.Slot].VideoID
	log.Info().Str("room_id", client.RoomID).Int("slot", payload.Slot).Str("video_id", videoID).Msg("soundpad play triggered")
	broadcaster.BroadcastSoundPadPlay(h, client.RoomID, payload.Slot, videoID, client.ID)
}
