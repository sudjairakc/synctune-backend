// Package controller มี Business Logic สำหรับจัดการ Queue
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
	"github.com/synctune/backend/tiktok"
	"github.com/synctune/backend/youtube"
)

// เมิน song_ended ของคลิป broadcast ถ้าเริ่มเล่นจริงในเชิง wall clock ได้ไม่เกินเท่านี้ (กัน client ส่ง ENDED จากคนละวิดีโอใน iframe)
const minBroadcastWallBeforeSongEndedSec int64 = 4

// addSongPayload คือ Payload ของ event add_song
// video_url รองรับทั้ง YouTube และ TikTok; youtube_url ไว้ backward compat
type addSongPayload struct {
	VideoURL   string `json:"video_url"`
	YoutubeURL string `json:"youtube_url"`
	AddedBy    string `json:"added_by"`
}

// removeSongPayload คือ Payload ของ event remove_song
type removeSongPayload struct {
	SongID string `json:"song_id"`
}

// reorderQueuePayload คือ Payload ของ event reorder_queue
type reorderQueuePayload struct {
	SongID   string `json:"song_id"`
	NewIndex int    `json:"new_index"`
}

// reportErrorPayload คือ Payload ของ event report_error
type reportErrorPayload struct {
	SongID    string `json:"song_id"`
	ErrorCode int    `json:"error_code"`
}

// setPlaybackModePayload คือ Payload ของ event set_playback_mode
type setPlaybackModePayload struct {
	Autoplay   *bool `json:"autoplay"`
	Shuffle    *bool `json:"shuffle"`
	RandomPlay *bool `json:"random_play"`
}

var youtubeVideoIDRegex = regexp.MustCompile(`^[\w-]{11}$`)

// requireJoined ตรวจว่า Client join แล้ว คืน false และส่ง error ถ้ายังไม่ join
func requireJoined(h *hub.Hub, client *hub.Client) bool {
	if client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "NOT_JOINED",
			Message: "ต้องส่ง join ก่อน",
		})
		return false
	}
	return true
}

// HandleAddSong จัดการ Event add_song
func HandleAddSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	if !client.AddSongLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "RATE_LIMITED",
			Message: "เพิ่มเพลงบ่อยเกินไป กรุณารอสักครู่",
		})
		return
	}

	var payload addSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	rawURL := strings.TrimSpace(payload.VideoURL)
	if rawURL == "" {
		rawURL = strings.TrimSpace(payload.YoutubeURL)
	}
	if rawURL == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "กรุณาใส่ URL"})
		return
	}

	var videoID, platform string
	if tiktok.IsValidURL(rawURL) {
		id, err := tiktok.ExtractVideoID(rawURL)
		if err != nil {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "ไม่สามารถดึง TikTok Video ID จาก URL ได้"})
			return
		}
		videoID = id
		platform = "tiktok"
	} else {
		if !isValidYouTubeURL(rawURL) {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "URL ไม่ถูกต้อง กรุณาใช้ YouTube หรือ TikTok URL"})
			return
		}
		id, err := extractVideoID(rawURL)
		if err != nil {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "ไม่สามารถดึง Video ID จาก URL ได้"})
			return
		}
		videoID = id
		platform = "youtube"
	}

	addedBy := client.User.Username
	if addedBy == "" {
		addedBy = strings.TrimSpace(payload.AddedBy)
	}
	if addedBy == "" {
		addedBy = "Anonymous"
	}
	if len([]rune(addedBy)) > 30 {
		addedBy = string([]rune(addedBy)[:30])
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleAddSong: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	if len(state.CurrentQueue) >= maxQueueSize(h) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "QUEUE_FULL", Message: "คิวเต็มแล้ว"})
		return
	}

	var songTitle, songThumbnail string
	var isLive bool
	if platform == "tiktok" {
		tikMeta, err := tiktok.FetchMetadata(videoID)
		if err != nil {
			log.Warn().Err(err).Str("video_id", videoID).Msg("HandleAddSong: failed to fetch tiktok metadata, using fallback")
			songTitle = videoID
			songThumbnail = ""
		} else {
			songTitle = tikMeta.Title
			songThumbnail = tikMeta.Thumbnail
		}
	} else {
		meta, err := youtube.FetchMetadata(videoID)
		if err != nil {
			log.Warn().Err(err).Str("video_id", videoID).Msg("HandleAddSong: failed to fetch metadata, using fallback")
			songTitle = videoID
			songThumbnail = "https://i.ytimg.com/vi/" + videoID + "/hqdefault.jpg"
		} else {
			songTitle = meta.Title
			songThumbnail = meta.Thumbnail
			isLive = isLiveYouTubeURL(rawURL) || meta.LikelyBroadcastLive
		}
	}

	song := model.Song{
		QueueID:   uuid.New().String(),
		ID:        videoID,
		Title:     songTitle,
		Thumbnail: songThumbnail,
		AddedBy:   addedBy,
		Platform:  platform,
		IsLive:    isLive,
	}
	state.CurrentQueue = append(state.CurrentQueue, song)

	if !state.IsPlaying && len(state.CurrentQueue) == 1 {
		state.IsPlaying = true
		state.CurrentIndex = 0
		state.SeekTime = 0
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleAddSong: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "add_song").Str("room_id", roomID).Str("song_id", song.ID).Str("added_by", song.AddedBy).Msg("song added to queue")
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleRemoveSong จัดการ Event remove_song — เริ่ม vote ก่อนลบ
func HandleRemoveSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload removeSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleRemoveSong: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	removeIdx := findSongIndex(state.CurrentQueue, payload.SongID)
	if removeIdx == -1 {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SONG_NOT_FOUND", Message: "ไม่พบเพลงในคิว"})
		return
	}
	if removeIdx == state.CurrentIndex {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "CANNOT_REMOVE_CURRENT", Message: "ไม่สามารถลบเพลงที่กำลังเล่นอยู่ได้"})
		return
	}

	song := state.CurrentQueue[removeIdx]
	if song.AddedBy == client.User.Username {
		executeRemoveSong(h, client, payload.SongID)
		return
	}
	executed, err := startVote(h, client, model.VoteActionRemoveSong, song.QueueID, song.Title)
	if err != nil {
		log.Error().Err(err).Msg("HandleRemoveSong: failed to start vote")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}
	if executed {
		executeRemoveSong(h, client, payload.SongID)
	}
}

// executeRemoveSong ลบเพลงจริงหลัง vote ผ่าน
func executeRemoveSong(h *hub.Hub, client *hub.Client, songQueueID string) {
	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("executeRemoveSong: failed to get state")
		return
	}

	removeIdx := findSongIndex(state.CurrentQueue, songQueueID)
	if removeIdx == -1 || removeIdx == state.CurrentIndex {
		return
	}

	removedSong := state.CurrentQueue[removeIdx]
	state.CurrentQueue = append(state.CurrentQueue[:removeIdx], state.CurrentQueue[removeIdx+1:]...)
	if removeIdx < state.CurrentIndex {
		state.CurrentIndex--
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("executeRemoveSong: failed to set state")
		return
	}

	log.Info().Str("event", "remove_song").Str("room_id", roomID).Str("song_id", songQueueID).Str("by_user_id", client.User.ID).Str("by_username", client.User.Username).Msg("song removed from queue")
	broadcaster.BroadcastRoomAction(h, roomID, "remove_song", client.User.Username, removedSong.Title)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleReorderQueue จัดการ Event reorder_queue
func HandleReorderQueue(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload reorderQueuePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleReorderQueue: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	fromIdx := findSongIndex(state.CurrentQueue, payload.SongID)
	if fromIdx == -1 {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SONG_NOT_FOUND", Message: "ไม่พบเพลงในคิว"})
		return
	}

	toIdx := payload.NewIndex
	if toIdx < 0 || toIdx >= len(state.CurrentQueue) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_INDEX", Message: "ตำแหน่งไม่ถูกต้อง"})
		return
	}

	song := state.CurrentQueue[fromIdx]
	newQueue := make([]model.Song, 0, len(state.CurrentQueue))
	for i, s := range state.CurrentQueue {
		if i == fromIdx {
			continue
		}
		newQueue = append(newQueue, s)
	}
	newQueue = append(newQueue[:toIdx], append([]model.Song{song}, newQueue[toIdx:]...)...)
	state.CurrentQueue = newQueue

	if fromIdx == state.CurrentIndex {
		state.CurrentIndex = toIdx
	} else if fromIdx < state.CurrentIndex && toIdx >= state.CurrentIndex {
		state.CurrentIndex--
	} else if fromIdx > state.CurrentIndex && toIdx <= state.CurrentIndex {
		state.CurrentIndex++
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleReorderQueue: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "reorder_queue").Str("room_id", roomID).Str("song_id", payload.SongID).Int("from_index", fromIdx).Int("new_index", toIdx).Str("by_user_id", client.User.ID).Str("by_username", client.User.Username).Msg("queue reordered")
	broadcaster.BroadcastRoomAction(h, roomID, "reorder_queue", client.User.Username, song.Title)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleReportError จัดการ Event report_error (YouTube Error 101/150)
func HandleReportError(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	if !client.ReportErrorLimiter.Allow() {
		return
	}

	var payload reportErrorPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	if payload.ErrorCode != 101 && payload.ErrorCode != 150 {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_ERROR_CODE", Message: "error_code ต้องเป็น 101 หรือ 150 เท่านั้น"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}

	currentSong := state.CurrentQueue[state.CurrentIndex]
	if payload.SongID != currentSong.QueueID {
		return
	}

	if err := h.Store().PushHistory(ctx, roomID, model.HistorySong{Song: currentSong, Status: "skipped"}); err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to push history")
	}

	broadcaster.BroadcastSongSkipped(h, roomID, currentSong, payload.ErrorCode)

	if currentSong.IsBroadcast {
		state.SeekTime = 0
		if len(state.BroadcastQueue) > 0 {
			next := state.BroadcastQueue[0]
			state.BroadcastQueue = state.BroadcastQueue[1:]
			state.CurrentQueue = []model.Song{next}
			state.CurrentIndex = 0
			state.IsPlaying = true
			state.BroadcastPlaybackStartedUnix = time.Now().Unix()
		} else {
			state.IsBroadcasting = false
			state.BroadcastPlaybackStartedUnix = 0
			state.CurrentQueue = state.SavedQueue
			state.CurrentIndex = state.SavedCurrentIndex
			state.SeekTime = state.SavedSeekTime
			state.IsPlaying = state.SavedIsPlaying
			state.SavedQueue = nil
			state.SavedIsPlaying = false
		}
		if err := h.Store().SetState(ctx, roomID, state); err != nil {
			log.Error().Err(err).Msg("HandleReportError(broadcast): failed to set state")
			return
		}
		log.Info().Str("event", "report_error").Str("room_id", roomID).Str("song_id", payload.SongID).Int("error_code", payload.ErrorCode).Bool("is_broadcast", true).Msg("broadcast skipped due to error")
		broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
		return
	}

	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if state.CurrentIndex >= len(state.CurrentQueue) {
		state.IsPlaying = false
		state.CurrentIndex = 0
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to set state")
		return
	}

	log.Info().Str("event", "report_error").Str("room_id", roomID).Str("song_id", payload.SongID).Int("error_code", payload.ErrorCode).Msg("song skipped due to error")
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// songEndedPayload คือ Payload ของ event song_ended
type songEndedPayload struct {
	SongID string `json:"song_id"`
}

// skipSongPayload คือ Payload ของ event skip_song
type skipSongPayload struct {
	SongID string `json:"song_id"`
}

// HandleSkipSong จัดการ Event skip_song — เริ่ม vote ก่อนข้าม
func HandleSkipSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload skipSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}
	currentSong := state.CurrentQueue[state.CurrentIndex]
	if payload.SongID != currentSong.QueueID {
		return
	}

	// ระหว่าง broadcast replay — ล็อก skip สำหรับ user ทั่วไป (admin ข้ามผ่าน HTTP endpoint)
	if currentSong.IsBroadcast && state.BroadcastSkipLocked {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "BROADCAST_LOCKED", Message: "ไม่สามารถข้ามได้ระหว่าง broadcast replay"})
		return
	}

	if currentSong.IsBroadcast {
		settings, err := h.Store().GetSettings(ctx)
		if err != nil {
			settings = &model.AppSettings{}
		}
		if !settings.AllowSkipBroadcast {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "BROADCAST_SKIP_DISABLED", Message: "admin ปิดการ skip broadcast"})
			return
		}
		if currentSong.Duration > 0 && state.SeekTime >= currentSong.Duration/2 {
			h.SendToSession(client.Conn, "error", model.WSError{Code: "BROADCAST_SKIP_TOO_LATE", Message: "ไม่สามารถ skip ได้หลังจากเล่นเกิน 50% ของวิดีโอแล้ว"})
			return
		}
		executed, err := startVote(h, client, model.VoteActionSkipBroadcast, currentSong.QueueID, currentSong.Title)
		if err != nil {
			log.Error().Err(err).Msg("HandleSkipSong: failed to start broadcast vote")
			return
		}
		if executed {
			executeSkipSong(h, client, payload.SongID)
		}
		return
	}

	if currentSong.AddedBy == client.User.Username {
		executeSkipSong(h, client, payload.SongID)
		return
	}
	executed, err := startVote(h, client, model.VoteActionSkipSong, currentSong.QueueID, currentSong.Title)
	if err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to start vote")
		return
	}
	if executed {
		executeSkipSong(h, client, payload.SongID)
	}
}

// executeSkipSong ข้ามเพลงจริงหลัง vote ผ่าน
func executeSkipSong(h *hub.Hub, client *hub.Client, songQueueID string) {
	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("executeSkipSong: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}
	currentSong := state.CurrentQueue[state.CurrentIndex]
	if songQueueID != currentSong.QueueID {
		return
	}

	if err := h.Store().PushHistory(ctx, roomID, model.HistorySong{Song: currentSong, Status: "skipped", SkippedBy: client.User.Username}); err != nil {
		log.Error().Err(err).Msg("executeSkipSong: failed to push history")
	}

	broadcaster.BroadcastSongSkipped(h, roomID, currentSong, 0)

	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if len(state.CurrentQueue) > 0 {
		state.IsPlaying = true
		if state.RandomPlay {
			state.CurrentIndex = pseudoRandIntn(len(state.CurrentQueue))
		} else if state.CurrentIndex >= len(state.CurrentQueue) {
			state.CurrentIndex = 0
		}
	} else {
		state.IsPlaying = false
		state.CurrentIndex = 0
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("executeSkipSong: failed to set state")
		return
	}

	log.Info().Str("event", "skip_song").Str("room_id", roomID).Str("song_id", currentSong.ID).Str("by_username", client.User.Username).Msg("song skipped by vote")
	broadcaster.BroadcastRoomAction(h, roomID, "skip_song", client.User.Username, currentSong.Title)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleSongEnded จัดการ Event song_ended
func HandleSongEnded(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload songEndedPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}

	currentSong := state.CurrentQueue[state.CurrentIndex]
	if payload.SongID != currentSong.QueueID {
		return
	}

	if currentSong.IsBroadcast && state.BroadcastPlaybackStartedUnix > 0 {
		since := time.Now().Unix() - state.BroadcastPlaybackStartedUnix
		if since < minBroadcastWallBeforeSongEndedSec {
			log.Debug().Str("room_id", roomID).Str("queue_id", currentSong.QueueID).Int64("since_started_sec", since).
				Msg("HandleSongEnded: broadcast song_ended ignored (too soon after segment start)")
			return
		}
	}

	claimed, err := h.Store().ClaimSongEnded(ctx, roomID, currentSong.QueueID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to claim")
		return
	}
	if !claimed {
		log.Debug().Str("room_id", roomID).Str("queue_id", currentSong.QueueID).Msg("HandleSongEnded: duplicate, ignored")
		return
	}

	if err := h.Store().PushHistory(ctx, roomID, model.HistorySong{Song: currentSong, Status: "played"}); err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to push history")
	}

	// broadcast song จบ — replay ครั้งแรก (ถ้า vote ไม่ผ่าน) หรือ restore หลัง replay
	if currentSong.IsBroadcast {
		state.SeekTime = 0
		if len(state.BroadcastQueue) > 0 {
			// มี broadcast ถัดไปรอ
			next := state.BroadcastQueue[0]
			state.BroadcastQueue = state.BroadcastQueue[1:]
			state.CurrentQueue = []model.Song{next}
			state.CurrentIndex = 0
			state.IsPlaying = true
			state.BroadcastPlaybackStartedUnix = time.Now().Unix()
			state.BroadcastVoteReplayDone = false
			state.BroadcastSkipLocked = false
		} else if !state.BroadcastVoteReplayDone {
			// รอบแรกจบ — vote ไม่ผ่าน ให้เล่นซ้ำ 1 รอบ (locked)
			state.BroadcastVoteReplayDone = true
			state.BroadcastSkipLocked = true
			state.BroadcastPlaybackStartedUnix = time.Now().Unix()
			// เปลี่ยน queue_id ของเพลง replay → client เห็นเป็น "เพลงใหม่" จึง reload เล่นซ้ำจริง
			// ผลพลอยได้: reset ตัวกันส่ง song_ended ซ้ำ (songEndedSent) ฝั่ง client → ตอน replay จบจะส่ง song_ended ได้ → restore
			// ถ้าใช้ queue_id เดิม client จะไม่ reload และไม่ส่ง song_ended รอบสอง → ค้างที่ broadcast
			replaySong := state.CurrentQueue[state.CurrentIndex]
			replaySong.QueueID = replaySong.QueueID + "_replay"
			state.CurrentQueue[state.CurrentIndex] = replaySong
			if err := h.Store().SetState(ctx, roomID, state); err != nil {
				log.Error().Err(err).Msg("HandleSongEnded(broadcast replay): failed to set state")
				return
			}
			// skip vote สำหรับเพลงนี้หมดความหมายแล้ว — ปิด modal ทุก client ทันที
			clearActiveVote(h, roomID)
			log.Info().Str("event", "song_ended").Str("room_id", roomID).Str("song_id", currentSong.ID).Msg("broadcast: vote not passed, replaying once (locked)")
			broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
			h.BroadcastToRoom(roomID, "broadcast_replay", struct{}{})
			return
		} else {
			// replay จบ — restore state
			state.IsBroadcasting = false
			state.BroadcastPlaybackStartedUnix = 0
			state.BroadcastVoteReplayDone = false
			state.BroadcastSkipLocked = false
			state.CurrentQueue = state.SavedQueue
			state.CurrentIndex = state.SavedCurrentIndex
			state.SeekTime = state.SavedSeekTime
			state.IsPlaying = state.SavedIsPlaying
			state.SavedQueue = nil
			state.SavedIsPlaying = false
		}
		if err := h.Store().SetState(ctx, roomID, state); err != nil {
			log.Error().Err(err).Msg("HandleSongEnded(broadcast): failed to set state")
			return
		}
		// vote ที่ค้างกับเพลง broadcast เดิมหมดความหมายแล้ว — ปิด modal ทุก client (no-op ถ้าไม่มี vote)
		clearActiveVote(h, roomID)
		log.Info().Str("event", "song_ended").Str("room_id", roomID).Str("song_id", currentSong.ID).Bool("is_broadcast", true).Msg("broadcast ended")
		broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
		return
	}

	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if state.Autoplay && len(state.CurrentQueue) > 0 {
		state.IsPlaying = true
		switch {
		case state.RandomPlay:
			state.CurrentIndex = pseudoRandIntn(len(state.CurrentQueue))
		default:
			if state.CurrentIndex >= len(state.CurrentQueue) {
				state.CurrentIndex = 0
			}
		}
	} else {
		state.IsPlaying = false
		state.CurrentIndex = 0
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to set state")
		return
	}

	log.Info().Str("event", "song_ended").Str("room_id", roomID).Str("song_id", currentSong.ID).Bool("autoplay", state.Autoplay).Msg("song ended")
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleSetPlaybackMode จัดการ Event set_playback_mode
func HandleSetPlaybackMode(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload setPlaybackModePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID
	state, err := h.Store().GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackMode: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	if payload.Autoplay != nil {
		state.Autoplay = *payload.Autoplay
	}
	if payload.Shuffle != nil {
		state.Shuffle = *payload.Shuffle
	}
	if payload.RandomPlay != nil {
		state.RandomPlay = *payload.RandomPlay
	}

	if state.Shuffle && state.RandomPlay {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_PLAYBACK_MODE",
			Message: "ไม่สามารถเปิด Shuffle และ Random Play พร้อมกันได้",
		})
		return
	}

	if state.Shuffle {
		shuffleQueueAfterCurrent(state)
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackMode: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "set_playback_mode").Str("room_id", roomID).Bool("autoplay", state.Autoplay).Bool("shuffle", state.Shuffle).Bool("random_play", state.RandomPlay).Str("by_user_id", client.User.ID).Str("by_username", client.User.Username).Msg("playback mode updated")
	if modeDetail := buildModeDetail(payload); modeDetail != "" {
		broadcaster.BroadcastRoomAction(h, roomID, "set_playback_mode", client.User.Username, modeDetail)
	}
	broadcaster.BroadcastPlaybackModeUpdated(h, roomID, state)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
}

// HandleSetPlaybackSpeed จัดการ Event set_playback_speed — persist + broadcast ไปทั้งห้อง
func HandleSetPlaybackSpeed(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	var payload struct {
		Speed float64 `json:"speed"`
	}
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return
	}
	// รับค่าที่ YouTube IFrame API รองรับ: 0.25, 0.5, 1, 1.5, 2
	allowed := map[float64]bool{0.25: true, 0.5: true, 1: true, 1.5: true, 2: true}
	if !allowed[payload.Speed] {
		return
	}
	ctx := context.Background()
	state, err := h.Store().GetState(ctx, client.RoomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackSpeed: failed to get state")
		return
	}
	state.PlaybackSpeed = payload.Speed
	if err := h.Store().SetState(ctx, client.RoomID, state); err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackSpeed: failed to save state")
		return
	}
	log.Info().Str("room_id", client.RoomID).Float64("speed", payload.Speed).Str("by_user_id", client.User.ID).Str("by_username", client.User.Username).Msg("playback speed changed")
	broadcaster.BroadcastRoomAction(h, client.RoomID, "set_playback_speed", client.User.Username, fmt.Sprintf("%.2gx", payload.Speed))
	h.BroadcastToRoom(client.RoomID, "playback_speed_updated", map[string]float64{"speed": payload.Speed})
}

func buildModeDetail(p setPlaybackModePayload) string {
	onOff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	var parts []string
	if p.Autoplay != nil {
		parts = append(parts, "autoplay "+onOff(*p.Autoplay))
	}
	if p.Shuffle != nil {
		parts = append(parts, "shuffle "+onOff(*p.Shuffle))
	}
	if p.RandomPlay != nil {
		parts = append(parts, "random "+onOff(*p.RandomPlay))
	}
	return strings.Join(parts, ", ")
}

// fetchHistoryOrEmpty ดึง History จาก Store คืน slice ว่างถ้า error
func fetchHistoryOrEmpty(ctx context.Context, s store.Store, roomID string) []model.HistorySong {
	history, err := s.GetHistory(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("fetchHistoryOrEmpty: failed to get history")
		return []model.HistorySong{}
	}
	return history
}

// shuffleQueueAfterCurrent สลับ songs หลัง CurrentIndex ใน-place (Fisher-Yates)
func shuffleQueueAfterCurrent(state *model.PlaylistState) {
	start := state.CurrentIndex + 1
	n := len(state.CurrentQueue)
	if start >= n {
		return
	}
	tail := state.CurrentQueue[start:]
	for i := len(tail) - 1; i > 0; i-- {
		j := pseudoRandIntn(i + 1)
		tail[i], tail[j] = tail[j], tail[i]
	}
}

// pseudoRandIntn คืน random int [0, n) โดยใช้ time-based xorshift
var xorshiftState uint64 = 0x9e3779b97f4a7c15

func pseudoRandIntn(n int) int {
	xorshiftState ^= xorshiftState << 13
	xorshiftState ^= xorshiftState >> 7
	xorshiftState ^= xorshiftState << 17
	return int(xorshiftState>>1) % n
}

// --- Helper Functions ---

func extractVideoID(rawURL string) (string, error) {
	if rawURL == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", model.ErrInvalidURL
	}

	var videoID string
	switch u.Host {
	case "www.youtube.com", "youtube.com", "m.youtube.com", "music.youtube.com":
		if id, ok := strings.CutPrefix(u.Path, "/shorts/"); ok {
			videoID = id
		} else if id, ok := strings.CutPrefix(u.Path, "/live/"); ok {
			videoID = id
		} else {
			videoID = u.Query().Get("v")
		}
	case "youtu.be":
		videoID = strings.TrimPrefix(u.Path, "/")
	default:
		return "", model.ErrInvalidURL
	}

	if !youtubeVideoIDRegex.MatchString(videoID) {
		return "", model.ErrInvalidURL
	}
	return videoID, nil
}

func isValidYouTubeURL(rawURL string) bool {
	_, err := extractVideoID(rawURL)
	return err == nil
}

func isLiveYouTubeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	// เช่น /live/VIDEO_ID หรือ /channel/.../live
	if strings.Contains(u.Path, "/live") {
		return true
	}
	switch u.Query().Get("live") {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// findSongIndex หา Index ของ Song ใน Queue จาก QueueID
func findSongIndex(queue []model.Song, queueID string) int {
	for i, s := range queue {
		if s.QueueID == queueID {
			return i
		}
	}
	return -1
}

func maxQueueSize(_ *hub.Hub) int {
	return 100
}
