// Package controller — sound pad handlers
package controller

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/tiktok"
)

const maxSoundPadTitleLen = 100

type soundPadSetPayload struct {
	Slot     int    `json:"slot"`
	VideoID  string `json:"video_id"`
	VideoURL string `json:"video_url"` // short URL สำหรับ TikTok — backend resolve เป็น video_id
	Title    string `json:"title"`
	Platform string `json:"platform"` // "youtube" | "tiktok"
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
	isTikTok := payload.Slot >= model.TikTokSlotStart
	if isTikTok {
		// ถ้าส่ง video_url มา ให้ extract/resolve video ID ก่อน
		if payload.VideoID == "" && payload.VideoURL != "" {
			id, err := tiktok.ExtractVideoID(payload.VideoURL)
			if err != nil {
				h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "ไม่สามารถดึง TikTok Video ID จาก URL ได้"})
				return
			}
			payload.VideoID = id
			if payload.Title == "" {
				payload.Title = "TikTok " + id
			}
		}
		// TikTok video ID คือตัวเลข 1–25 หลัก
		if len(payload.VideoID) < 1 || len(payload.VideoID) > 25 {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "ไม่สามารถดึง TikTok Video ID จาก URL ได้"})
			return
		}
		payload.Platform = "tiktok"
	} else {
		// YouTube video ID = 11 ตัวอักษรเสมอ
		if len(payload.VideoID) != 11 {
			return
		}
		payload.Platform = "youtube"
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
	pad[payload.Slot] = &model.SoundPadSlot{VideoID: payload.VideoID, Title: payload.Title, Platform: payload.Platform}
	if err := h.Store().SetSoundPad(ctx, client.RoomID, pad); err != nil {
		log.Error().Err(err).Msg("HandleSoundPadSet: failed to save pad")
		return
	}
	log.Info().Str("room_id", client.RoomID).Int("slot", payload.Slot).Str("video_id", payload.VideoID).Str("by_username", client.User.Username).Msg("soundpad slot set")
	broadcaster.BroadcastRoomAction(h, client.RoomID, "soundpad_set", client.User.Username, payload.Title)
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
	oldTitle := ""
	if pad[payload.Slot] != nil {
		oldTitle = pad[payload.Slot].Title
	}
	pad[payload.Slot] = nil
	if err := h.Store().SetSoundPad(ctx, client.RoomID, pad); err != nil {
		log.Error().Err(err).Msg("HandleSoundPadClear: failed to save pad")
		return
	}
	log.Info().Str("room_id", client.RoomID).Int("slot", payload.Slot).Str("by_username", client.User.Username).Msg("soundpad slot cleared")
	broadcaster.BroadcastRoomAction(h, client.RoomID, "soundpad_clear", client.User.Username, oldTitle)
	broadcaster.BroadcastSoundPadUpdated(h, client.RoomID, pad)
}

// HandleSoundPadStop broadcast stop ไปทั้งห้อง — ไม่แตะ Redis
func HandleSoundPadStop(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	log.Info().Str("room_id", client.RoomID).Str("username", client.User.Username).Msg("soundpad stop triggered")
	broadcaster.BroadcastRoomAction(h, client.RoomID, "soundpad_stop", client.User.Username, "")
	broadcaster.BroadcastSoundPadStop(h, client.RoomID)
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
	slot := pad[payload.Slot]
	log.Info().
		Str("room_id", client.RoomID).
		Str("username", client.User.Username).
		Int("slot", payload.Slot).
		Str("video_id", slot.VideoID).
		Str("title", slot.Title).
		Msg("soundpad play triggered")

	ev := model.SoundPadPlayEvent{
		Slot:      payload.Slot,
		VideoID:   slot.VideoID,
		Title:     slot.Title,
		PlayedBy:  client.User.Username,
		UserID:    client.User.ID,
		Timestamp: time.Now().UnixMilli(),
	}
	if err := h.Store().PushSoundPadPlay(ctx, client.RoomID, ev); err != nil {
		log.Error().Err(err).Msg("HandleSoundPadPlay: failed to push history")
	}
	broadcaster.BroadcastSoundPadPlay(h, client.RoomID, payload.Slot, slot.VideoID, client.ID)
}
