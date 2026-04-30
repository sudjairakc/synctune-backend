package model

import "encoding/json"

// Song แทนเพลง 1 รายการในคิว
type Song struct {
	QueueID     string `json:"queue_id"`               // Unique ID ต่อ queue slot (UUID) — ใช้สำหรับ remove/reorder/skip
	ID          string `json:"id"`                     // YouTube Video ID (11 chars) — ใช้สำหรับเล่นใน player
	Title       string `json:"title"`                  // ชื่อเพลง
	Thumbnail   string `json:"thumbnail"`              // Thumbnail URL (maxresdefault หรือ hqdefault)
	AddedBy     string `json:"added_by"`               // ชื่อผู้เพิ่ม
	Duration    int    `json:"duration"`               // ความยาว (วินาที), 0 = ไม่ทราบ
	IsBroadcast bool   `json:"is_broadcast,omitempty"` // true = เพลงนี้เป็น scheduled broadcast
	IsLive      bool   `json:"is_live,omitempty"`      // true = YouTube live stream (ไม่ seek)
}

// HistorySong แทนเพลงที่เล่นจบแล้วหรือถูก Skip
type HistorySong struct {
	Song
	Status    string `json:"status"`               // "played" | "skipped"
	SkippedBy string `json:"skipped_by,omitempty"` // username ของคนที่ skip (ว่างถ้าเป็น system)
}

// PlaylistState คือ State หลักของระบบทั้งหมด
type PlaylistState struct {
	CurrentQueue  []Song  `json:"current_queue"`
	CurrentIndex  int     `json:"current_index"`
	SeekTime      int     `json:"seek_time"`
	IsPlaying     bool    `json:"is_playing"`
	Autoplay      bool    `json:"autoplay"`
	Shuffle       bool    `json:"shuffle"`
	RandomPlay    bool    `json:"random_play"`
	PlaybackSpeed float64 `json:"playback_speed"`

	// Broadcast state — จัดการโดย broadcast/scheduler.go เท่านั้น
	IsBroadcasting               bool   `json:"is_broadcasting,omitempty"`
	BroadcastPlaybackStartedUnix int64  `json:"broadcast_pb_started_unix,omitempty"` // กัน song_ended หลอกเมื่อสลับวิดีโอ
	BroadcastQueue               []Song `json:"broadcast_queue,omitempty"`           // broadcasts ที่รอเล่น (queue)
	SavedQueue                   []Song `json:"saved_queue,omitempty"`               // queue ก่อน broadcast เริ่ม
	SavedCurrentIndex            int    `json:"saved_current_index,omitempty"`
	SavedSeekTime                int    `json:"saved_seek_time,omitempty"`
	SavedIsPlaying               bool   `json:"saved_is_playing,omitempty"`
}

// SoundPadSlot แทน 1 slot ของ Sound Pad (nil = ว่าง)
type SoundPadSlot struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
}

const SoundPadSize = 50

// SoundPadPlayEvent บันทึกการกด soundpad แต่ละครั้ง
type SoundPadPlayEvent struct {
	Slot      int    `json:"slot"`
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	PlayedBy  string `json:"played_by"` // username
	UserID    string `json:"user_id"`
	Timestamp int64  `json:"timestamp"` // Unix milliseconds
}

// WSMessage คือ Envelope ของทุก WebSocket Message
type WSMessage struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// WSError คือ Payload ของ event "error"
type WSError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BroadcastSchedule คือ 1 รายการ broadcast schedule ที่เก็บใน Redis
type BroadcastSchedule struct {
	ID         string `json:"id"`        // UUID
	CronExpr   string `json:"cron_expr"` // "MIN HOUR * * *" (Asia/Bangkok)
	YoutubeURL string `json:"youtube_url"`
	Label      string `json:"label"` // ชื่อแสดงผล (optional)
	Enabled    bool   `json:"enabled"`
}
