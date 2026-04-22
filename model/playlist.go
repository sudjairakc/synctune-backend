package model

import "encoding/json"

// Song แทนเพลง 1 รายการในคิว
type Song struct {
	QueueID   string `json:"queue_id"`  // Unique ID ต่อ queue slot (UUID) — ใช้สำหรับ remove/reorder/skip
	ID        string `json:"id"`        // YouTube Video ID (11 chars) — ใช้สำหรับเล่นใน player
	Title     string `json:"title"`     // ชื่อเพลง
	Thumbnail string `json:"thumbnail"` // Thumbnail URL (maxresdefault หรือ hqdefault)
	AddedBy   string `json:"added_by"`  // ชื่อผู้เพิ่ม
	Duration  int    `json:"duration"`  // ความยาว (วินาที), 0 = ไม่ทราบ
}

// HistorySong แทนเพลงที่เล่นจบแล้วหรือถูก Skip
type HistorySong struct {
	Song
	Status    string `json:"status"`              // "played" | "skipped"
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
}

// SoundPadSlot แทน 1 slot ของ Sound Pad (nil = ว่าง)
type SoundPadSlot struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
}

const SoundPadSize = 50

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
