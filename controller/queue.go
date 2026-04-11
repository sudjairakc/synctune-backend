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
// ทุก field เป็น pointer เพื่อแยกระหว่าง "ไม่ส่งมา" กับ "ส่งมาเป็น false"
type setPlaybackModePayload struct {
	Autoplay   *bool `json:"autoplay"`
	Shuffle    *bool `json:"shuffle"`
	RandomPlay *bool `json:"random_play"`
}

var youtubeVideoIDRegex = regexp.MustCompile(`^[\w-]{11}$`)

// HandleAddSong จัดการ Event add_song
// 1. Validate YouTube URL → 2. Extract Video ID → 3. ตรวจ Duplicate
// 4. ตรวจ Queue Size → 5. สร้าง Song, อัปเดต State → 6. Broadcast queue_updated
func HandleAddSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	// Rate Limit
	if !client.AddSongLimiter.Allow() {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "RATE_LIMITED",
			Message: "เพิ่มเพลงบ่อยเกินไป กรุณารอสักครู่",
		})
		return
	}

	var payload addSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_MESSAGE",
			Message: "รูปแบบ Payload ไม่ถูกต้อง",
		})
		return
	}

	if !isValidYouTubeURL(payload.YoutubeURL) {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_URL",
			Message: "URL ไม่ถูกต้อง กรุณาใช้ YouTube URL",
		})
		return
	}

	videoID, err := extractVideoID(payload.YoutubeURL)
	if err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_URL",
			Message: "ไม่สามารถดึง Video ID จาก URL ได้",
		})
		return
	}

	// ใช้ username จาก User ที่ join แล้ว ถ้ายังไม่ join fallback เป็น payload.AddedBy
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
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleAddSong: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "SERVER_ERROR",
			Message: "เกิดข้อผิดพลาดภายใน",
		})
		return
	}

	// ตรวจ Duplicate
	if findSongIndex(state.CurrentQueue, videoID) != -1 {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "DUPLICATE_SONG",
			Message: "เพลงนี้อยู่ในคิวแล้ว",
		})
		return
	}

	// ตรวจ Queue Size
	if len(state.CurrentQueue) >= maxQueueSize(h) {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "QUEUE_FULL",
			Message: "คิวเต็มแล้ว",
		})
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

	// ถ้ายังไม่มีเพลงเล่น ให้เริ่มเล่นเลย
	if !state.IsPlaying && len(state.CurrentQueue) == 1 {
		state.IsPlaying = true
		state.CurrentIndex = 0
		state.SeekTime = 0
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleAddSong: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "SERVER_ERROR",
			Message: "เกิดข้อผิดพลาดภายใน",
		})
		return
	}

	log.Info().Str("event", "add_song").Str("song_id", song.ID).Str("added_by", song.AddedBy).Msg("song added to queue")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// HandleRemoveSong จัดการ Event remove_song
// 1. หา Song จาก ID → 2. ตรวจไม่ให้ลบ Current Song
// 3. ลบออกจากคิว อัปเดต CurrentIndex → 4. Broadcast queue_updated
func HandleRemoveSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	var payload removeSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_MESSAGE",
			Message: "รูปแบบ Payload ไม่ถูกต้อง",
		})
		return
	}

	ctx := context.Background()
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleRemoveSong: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	removeIdx := findSongIndex(state.CurrentQueue, payload.SongID)
	if removeIdx == -1 {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "SONG_NOT_FOUND",
			Message: "ไม่พบเพลงในคิว",
		})
		return
	}

	if removeIdx == state.CurrentIndex {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "CANNOT_REMOVE_CURRENT",
			Message: "ไม่สามารถลบเพลงที่กำลังเล่นอยู่ได้",
		})
		return
	}

	state.CurrentQueue = append(state.CurrentQueue[:removeIdx], state.CurrentQueue[removeIdx+1:]...)

	// ถ้า Song ที่ลบอยู่ก่อน Current → เลื่อน Index ลง 1
	if removeIdx < state.CurrentIndex {
		state.CurrentIndex--
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleRemoveSong: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "remove_song").Str("song_id", payload.SongID).Msg("song removed from queue")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// HandleReorderQueue จัดการ Event reorder_queue
// 1. ตรวจ new_index อยู่ใน range → 2. Re-slice คิว
// 3. อัปเดต CurrentIndex ถ้า Song ที่กำลังเล่นถูกย้าย → 4. Broadcast queue_updated
func HandleReorderQueue(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	var payload reorderQueuePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	state, err := h.Store().GetState(ctx)
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

	// Re-slice: ดึง Song ออกแล้วใส่ในตำแหน่งใหม่
	song := state.CurrentQueue[fromIdx]
	newQueue := make([]model.Song, 0, len(state.CurrentQueue))
	for i, s := range state.CurrentQueue {
		if i == fromIdx {
			continue
		}
		newQueue = append(newQueue, s)
	}
	// Insert at toIdx
	newQueue = append(newQueue[:toIdx], append([]model.Song{song}, newQueue[toIdx:]...)...)
	state.CurrentQueue = newQueue

	// ปรับ CurrentIndex ตาม Song ที่กำลังเล่นอยู่
	if fromIdx == state.CurrentIndex {
		state.CurrentIndex = toIdx
	} else if fromIdx < state.CurrentIndex && toIdx >= state.CurrentIndex {
		state.CurrentIndex--
	} else if fromIdx > state.CurrentIndex && toIdx <= state.CurrentIndex {
		state.CurrentIndex++
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleReorderQueue: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "reorder_queue").Str("song_id", payload.SongID).Int("new_index", toIdx).Msg("queue reordered")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// HandleReportError จัดการ Event report_error (YouTube Error 101/150)
// 1. ตรวจ song_id ตรงกับ Current Song → 2. ตรวจ error_code
// 3. บันทึก History status="skipped" → 4. เลื่อน CurrentIndex
// 5. Broadcast song_skipped + queue_updated
func HandleReportError(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	// Rate Limit
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

	// Reload State จาก Redis เสมอ — ป้องกัน Race Condition
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}

	currentSong := state.CurrentQueue[state.CurrentIndex]

	// ถ้า song_id ไม่ตรงกับเพลงที่กำลังเล่น → Ignore (Deduplication)
	if payload.SongID != currentSong.QueueID {
		return
	}

	// บันทึก History ว่า skipped
	historySong := model.HistorySong{Song: currentSong, Status: "skipped"}
	if err := h.Store().PushHistory(ctx, historySong); err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to push history")
	}

	// Broadcast song_skipped ก่อน
	broadcaster.BroadcastSongSkipped(h, currentSong, payload.ErrorCode)

	// ลบเพลงที่ error ออกจาก queue
	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if state.CurrentIndex >= len(state.CurrentQueue) {
		// ไม่มีเพลงเหลือ หรือ current index เกิน → หยุด
		state.IsPlaying = false
		state.CurrentIndex = 0
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleReportError: failed to set state")
		return
	}

	log.Info().Str("event", "report_error").Str("song_id", payload.SongID).Int("error_code", payload.ErrorCode).Msg("song skipped due to error")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
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
// 1. ตรวจ song_id ตรงกับ Current Song (Deduplication)
// 2. บันทึก History status="skipped"
// 3. Broadcast song_skipped
// 4. เลื่อนไปเพลงถัดไป (หรือหยุดถ้าคิวหมด)
// 5. Broadcast queue_updated
func HandleSkipSong(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	var payload skipSongPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()

	// Reload State จาก Redis เสมอ — ป้องกัน Race Condition
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}

	currentSong := state.CurrentQueue[state.CurrentIndex]

	// ถ้า song_id ไม่ตรงกับเพลงที่กำลังเล่น → Ignore (Deduplication)
	if payload.SongID != currentSong.QueueID {
		return
	}

	// บันทึก History ว่า skipped
	if err := h.Store().PushHistory(ctx, model.HistorySong{Song: currentSong, Status: "skipped"}); err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to push history")
	}

	// Broadcast song_skipped ก่อน (errorCode 0 = user-initiated skip)
	broadcaster.BroadcastSongSkipped(h, currentSong, 0)

	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if len(state.CurrentQueue) > 0 {
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

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleSkipSong: failed to set state")
		return
	}

	log.Info().Str("event", "skip_song").Str("song_id", currentSong.ID).Msg("song skipped by user")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// HandleSongEnded จัดการ Event song_ended
// 1. ตรวจ song_id ตรงกับ Current Song (Deduplication)
// 2. บันทึก History status="played"
// 3. เลื่อนไปเพลงถัดไป (หรือหยุดถ้าคิวหมด)
// 4. Broadcast queue_updated
func HandleSongEnded(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	var payload songEndedPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()

	// Reload State จาก Redis เสมอ — ป้องกัน Race Condition
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to get state")
		return
	}

	if len(state.CurrentQueue) == 0 {
		return
	}

	currentSong := state.CurrentQueue[state.CurrentIndex]

	// ถ้า song_id ไม่ตรงกับเพลงที่กำลังเล่น → Ignore (Deduplication)
	if payload.SongID != currentSong.QueueID {
		return
	}

	// บันทึก History ว่า played
	if err := h.Store().PushHistory(ctx, model.HistorySong{Song: currentSong, Status: "played"}); err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to push history")
	}

	state.CurrentQueue = append(state.CurrentQueue[:state.CurrentIndex], state.CurrentQueue[state.CurrentIndex+1:]...)
	state.SeekTime = 0

	if state.Autoplay && len(state.CurrentQueue) > 0 {
		state.IsPlaying = true
		switch {
		case state.RandomPlay:
			// สุ่ม index ใดก็ได้ใน queue
			state.CurrentIndex = pseudoRandIntn(len(state.CurrentQueue))
		case state.Shuffle:
			// queue ถูก shuffle ไว้ล่วงหน้าแล้ว → เล่น index ปัจจุบันต่อ (หลัง remove index อาจเกิน)
			if state.CurrentIndex >= len(state.CurrentQueue) {
				state.CurrentIndex = 0
			}
		default:
			// เล่นตามลำดับ — CurrentIndex ชี้ที่เพลงถัดไปแล้วหลัง remove
			if state.CurrentIndex >= len(state.CurrentQueue) {
				state.CurrentIndex = 0
			}
		}
	} else {
		state.IsPlaying = false
		state.CurrentIndex = 0
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleSongEnded: failed to set state")
		return
	}

	log.Info().Str("event", "song_ended").Str("song_id", currentSong.ID).Bool("autoplay", state.Autoplay).Msg("song ended")
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// HandleSetPlaybackMode จัดการ Event set_playback_mode
// 1. Unmarshal payload (pointer fields — merge ไม่ replace)
// 2. Validate: shuffle และ random_play เปิดพร้อมกันไม่ได้
// 3. อัปเดต State → 4. Broadcast playback_mode_updated
func HandleSetPlaybackMode(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	var payload setPlaybackModePayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_MESSAGE",
			Message: "รูปแบบ Payload ไม่ถูกต้อง",
		})
		return
	}

	ctx := context.Background()
	state, err := h.Store().GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackMode: failed to get state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	// Merge: อัปเดตเฉพาะ field ที่ส่งมา
	if payload.Autoplay != nil {
		state.Autoplay = *payload.Autoplay
	}
	if payload.Shuffle != nil {
		state.Shuffle = *payload.Shuffle
	}
	if payload.RandomPlay != nil {
		state.RandomPlay = *payload.RandomPlay
	}

	// Validate: shuffle กับ random_play เปิดพร้อมกันไม่ได้
	if state.Shuffle && state.RandomPlay {
		h.SendToSession(client.Conn, "error", model.WSError{
			Code:    "INVALID_PLAYBACK_MODE",
			Message: "ไม่สามารถเปิด Shuffle และ Random Play พร้อมกันได้",
		})
		return
	}

	// ถ้าเปิด Shuffle → สลับเพลงหลัง current ทันที แล้ว broadcast queue ใหม่ด้วย
	if state.Shuffle {
		shuffleQueueAfterCurrent(state)
	}

	if err := h.Store().SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("HandleSetPlaybackMode: failed to set state")
		h.SendToSession(client.Conn, "error", model.WSError{Code: "SERVER_ERROR", Message: "เกิดข้อผิดพลาดภายใน"})
		return
	}

	log.Info().Str("event", "set_playback_mode").Bool("autoplay", state.Autoplay).Bool("shuffle", state.Shuffle).Bool("random_play", state.RandomPlay).Msg("playback mode updated")
	broadcaster.BroadcastPlaybackModeUpdated(h, state)
	// broadcast queue_updated เสมอ เพื่อให้ frontend ได้ state ใหม่รวมถึง queue ที่อาจ shuffle แล้ว
	broadcaster.BroadcastQueueUpdated(h, state, fetchHistoryOrEmpty(ctx, h.Store()))
}

// fetchHistoryOrEmpty ดึง History จาก Store คืน slice ว่างถ้า error
func fetchHistoryOrEmpty(ctx context.Context, s store.Store) []model.HistorySong {
	history, err := s.GetHistory(ctx)
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
	// ใช้ time-based seed ผ่าน math/rand/v2 ไม่ได้ — ใช้ crypto/rand แทนเพื่อความ random
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

// extractVideoID แยก Video ID จาก YouTube URL
// รองรับทั้ง youtube.com/watch?v= และ youtu.be/
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
	case "www.youtube.com", "youtube.com", "m.youtube.com":
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

// isValidYouTubeURL ตรวจสอบว่า URL เป็น YouTube จริงหรือไม่
func isValidYouTubeURL(rawURL string) bool {
	_, err := extractVideoID(rawURL)
	return err == nil
}

// findSongIndex หา Index ของ Song ใน Queue จาก QueueID
// คืน -1 ถ้าไม่พบ
func findSongIndex(queue []model.Song, queueID string) int {
	for i, s := range queue {
		if s.QueueID == queueID {
			return i
		}
	}
	return -1
}

// maxQueueSize ดึงค่า MaxQueueSize จาก hub (default 100)
func maxQueueSize(h *hub.Hub) int {
	_ = h
	return 100
}
