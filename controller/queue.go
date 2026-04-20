// Package controller มี Business Logic สำหรับจัดการ Queue
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
	"github.com/synctune/backend/youtube"
)

// addSongPayload คือ Payload ของ event add_song
type addSongPayload struct {
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

	if !isValidYouTubeURL(payload.YoutubeURL) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "URL ไม่ถูกต้อง กรุณาใช้ YouTube URL"})
		return
	}

	videoID, err := extractVideoID(payload.YoutubeURL)
	if err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_URL", Message: "ไม่สามารถดึง Video ID จาก URL ได้"})
		return
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

	if findSongIndex(state.CurrentQueue, videoID) != -1 {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "DUPLICATE_SONG", Message: "เพลงนี้อยู่ในคิวแล้ว"})
		return
	}

	if len(state.CurrentQueue) >= maxQueueSize(h) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "QUEUE_FULL", Message: "คิวเต็มแล้ว"})
		return
	}

	meta, err := youtube.FetchMetadata(videoID)
	if err != nil {
		log.Warn().Err(err).Str("video_id", videoID).Msg("HandleAddSong: failed to fetch metadata, using fallback")
		meta = &youtube.VideoMetadata{
			Title:     videoID,
			Thumbnail: "https://i.ytimg.com/vi/" + videoID + "/hqdefault.jpg",
		}
	}

	song := model.Song{
		QueueID:   uuid.New().String(),
		ID:        videoID,
		Title:     meta.Title,
		Thumbnail: meta.Thumbnail,
		AddedBy:   addedBy,
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

// HandleRemoveSong จัดการ Event remove_song
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

	state.CurrentQueue = append(state.CurrentQueue[:removeIdx], state.CurrentQueue[removeIdx+1:]...)
	if removeIdx < state.CurrentIndex {
		state.CurrentIndex--
	}

	if err := h.Store().SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Msg("HandleRemoveSong: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "remove_song").Str("room_id", roomID).Str("song_id", payload.SongID).Msg("song removed from queue")
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

	log.Info().Str("event", "reorder_queue").Str("room_id", roomID).Str("song_id", payload.SongID).Int("new_index", toIdx).Msg("queue reordered")
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

// HandleSkipSong จัดการ Event skip_song (user กด ⏭)
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

	if err := h.Store().PushHistory(ctx, roomID, model.HistorySong{Song: currentSong, Status: "skipped"}); err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to push history")
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
		log.Error().Err(err).Msg("HandleSkipSong: failed to set state")
		return
	}

	log.Info().Str("event", "skip_song").Str("room_id", roomID).Str("song_id", currentSong.ID).Msg("song skipped by user")
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

	log.Info().Str("event", "set_playback_mode").Str("room_id", roomID).Bool("autoplay", state.Autoplay).Bool("shuffle", state.Shuffle).Bool("random_play", state.RandomPlay).Msg("playback mode updated")
	broadcaster.BroadcastPlaybackModeUpdated(h, roomID, state)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, fetchHistoryOrEmpty(ctx, h.Store(), roomID))
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
		videoID = u.Query().Get("v")
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
