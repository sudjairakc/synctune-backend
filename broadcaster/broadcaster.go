// Package broadcaster มี Helper Functions สำหรับ Broadcast WebSocket Events
package broadcaster

import (
	"encoding/json"

	"github.com/olahol/melody"
	"github.com/synctune/backend/model"
)

// soundPadUpdatedPayload คือ payload ของ event soundpad_updated
type soundPadUpdatedPayload struct {
	SoundPad []*model.SoundPadSlot `json:"sound_pad"`
}

// soundPadPlayPayload คือ payload ของ event soundpad_play
type soundPadPlayPayload struct {
	Slot    int    `json:"slot"`
	VideoID string `json:"video_id"`
	UserID  string `json:"user_id"`
}

// hubInterface ป้องกัน Circular import ระหว่าง hub และ broadcaster
type hubInterface interface {
	BroadcastToRoom(roomID string, event string, payload interface{})
	SendToSession(session *melody.Session, event string, payload interface{})
}

// queueUpdatedPayload คือ Payload ของ event queue_updated
type queueUpdatedPayload struct {
	CurrentQueue []model.Song        `json:"current_queue"`
	CurrentIndex int                 `json:"current_index"`
	SeekTime     int                 `json:"seek_time"`
	IsPlaying    bool                `json:"is_playing"`
	History      []model.HistorySong `json:"history"`
}

// seekSyncPayload คือ Payload ของ event seek_sync
type seekSyncPayload struct {
	SeekTime  int  `json:"seek_time"`
	IsPlaying bool `json:"is_playing"`
}

// songSkippedPayload คือ Payload ของ event song_skipped
type songSkippedPayload struct {
	SongID    string `json:"song_id"`
	Title     string `json:"title"`
	Reason    string `json:"reason"`
	ErrorCode int    `json:"error_code"`
}

// roomJoinedPayload คือ Payload ของ event room_joined
type roomJoinedPayload struct {
	RoomID       string                `json:"room_id"`
	CurrentQueue []model.Song          `json:"current_queue"`
	CurrentIndex int                   `json:"current_index"`
	SeekTime     int                   `json:"seek_time"`
	IsPlaying    bool                  `json:"is_playing"`
	Autoplay     bool                  `json:"autoplay"`
	Shuffle      bool                  `json:"shuffle"`
	RandomPlay   bool                  `json:"random_play"`
	History      []model.HistorySong   `json:"history"`
	ChatHistory  []model.ChatMessage   `json:"chat_history"`
	OnlineUsers   []model.User          `json:"online_users"`
	SoundPad        []*model.SoundPadSlot      `json:"sound_pad"`
	SoundPadHistory []model.SoundPadPlayEvent  `json:"soundpad_history"`
	PlaybackSpeed   float64                    `json:"playback_speed"`
}

// playbackModePayload คือ Payload ของ event playback_mode_updated
type playbackModePayload struct {
	Autoplay   bool `json:"autoplay"`
	Shuffle    bool `json:"shuffle"`
	RandomPlay bool `json:"random_play"`
}

// userEventPayload คือ Payload ของ event user_joined / user_left
type userEventPayload struct {
	User        model.User   `json:"user"`
	OnlineUsers []model.User `json:"online_users"`
}

// BroadcastQueueUpdated Broadcast event "queue_updated" ไปทุก Client ในห้อง
func BroadcastQueueUpdated(h hubInterface, roomID string, state *model.PlaylistState, history []model.HistorySong) {
	h.BroadcastToRoom(roomID, "queue_updated", queueUpdatedPayload{
		CurrentQueue: state.CurrentQueue,
		CurrentIndex: state.CurrentIndex,
		SeekTime:     state.SeekTime,
		IsPlaying:    state.IsPlaying,
		History:      history,
	})
}

// BroadcastSeekSync Broadcast event "seek_sync" ไปทุก Client ในห้อง
func BroadcastSeekSync(h hubInterface, roomID string, seekTime int, isPlaying bool) {
	h.BroadcastToRoom(roomID, "seek_sync", seekSyncPayload{
		SeekTime:  seekTime,
		IsPlaying: isPlaying,
	})
}

// BroadcastSongSkipped Broadcast event "song_skipped" ไปทุก Client ในห้อง
func BroadcastSongSkipped(h hubInterface, roomID string, song model.Song, errorCode int) {
	reason := "user_skipped"
	if errorCode == 101 {
		reason = "embed_not_allowed"
	} else if errorCode == 150 {
		reason = "embed_not_allowed_by_request"
	}
	h.BroadcastToRoom(roomID, "song_skipped", songSkippedPayload{
		SongID:    song.ID,
		Title:     song.Title,
		Reason:    reason,
		ErrorCode: errorCode,
	})
}

// SendRoomJoined ส่ง event "room_joined" ไปยัง Client ที่เพิ่ง join (ไม่ Broadcast)
func SendRoomJoined(h hubInterface, session *melody.Session, roomID string, state *model.PlaylistState, history []model.HistorySong, chatHistory []model.ChatMessage, onlineUsers []model.User, soundPad []*model.SoundPadSlot, soundPadHistory []model.SoundPadPlayEvent) {
	speed := state.PlaybackSpeed
	if speed == 0 {
		speed = 1
	}
	h.SendToSession(session, "room_joined", roomJoinedPayload{
		RoomID:        roomID,
		CurrentQueue:  state.CurrentQueue,
		CurrentIndex:  state.CurrentIndex,
		SeekTime:      state.SeekTime,
		IsPlaying:     state.IsPlaying,
		Autoplay:      state.Autoplay,
		Shuffle:       state.Shuffle,
		RandomPlay:    state.RandomPlay,
		History:       history,
		ChatHistory:   chatHistory,
		OnlineUsers:   onlineUsers,
		SoundPad:        soundPad,
		SoundPadHistory: soundPadHistory,
		PlaybackSpeed:   speed,
	})
}

// BroadcastPlaybackModeUpdated Broadcast event "playback_mode_updated" ไปทุก Client ในห้อง
func BroadcastPlaybackModeUpdated(h hubInterface, roomID string, state *model.PlaylistState) {
	h.BroadcastToRoom(roomID, "playback_mode_updated", playbackModePayload{
		Autoplay:   state.Autoplay,
		Shuffle:    state.Shuffle,
		RandomPlay: state.RandomPlay,
	})
}

// BroadcastUserJoined Broadcast event "user_joined" ไปทุก Client ในห้อง
func BroadcastUserJoined(h hubInterface, roomID string, user model.User, onlineUsers []model.User) {
	h.BroadcastToRoom(roomID, "user_joined", userEventPayload{User: user, OnlineUsers: onlineUsers})
}

// BroadcastUserLeft Broadcast event "user_left" ไปทุก Client ในห้อง
func BroadcastUserLeft(h hubInterface, roomID string, user model.User, onlineUsers []model.User) {
	h.BroadcastToRoom(roomID, "user_left", userEventPayload{User: user, OnlineUsers: onlineUsers})
}

// BroadcastMessageReceived Broadcast event "message_received" ไปทุก Client ในห้อง
func BroadcastMessageReceived(h hubInterface, roomID string, msg model.ChatMessage) {
	h.BroadcastToRoom(roomID, "message_received", msg)
}

// BroadcastSoundPadUpdated broadcast config ของ sound pad ไปทั้งห้อง
func BroadcastSoundPadUpdated(h hubInterface, roomID string, pad []*model.SoundPadSlot) {
	h.BroadcastToRoom(roomID, "soundpad_updated", soundPadUpdatedPayload{SoundPad: pad})
}

// BroadcastSoundPadPlay broadcast trigger เล่นเสียงไปทั้งห้อง
func BroadcastSoundPadPlay(h hubInterface, roomID string, slot int, videoID, userID string) {
	h.BroadcastToRoom(roomID, "soundpad_play", soundPadPlayPayload{Slot: slot, VideoID: videoID, UserID: userID})
}

// messageDeletedPayload คือ payload ของ event message_deleted
type messageDeletedPayload struct {
	MessageID string `json:"message_id"`
}

// messageReactedPayload คือ payload ของ event message_reacted
type messageReactedPayload struct {
	MessageID string              `json:"message_id"`
	Reactions map[string][]string `json:"reactions"`
}

// pinsUpdatedPayload คือ payload ของ event pins_updated
type pinsUpdatedPayload struct {
	Pins []model.ChatMessage `json:"pins"`
}

// BroadcastMessageDeleted broadcast event "message_deleted" ไปทุก Client ในห้อง
func BroadcastMessageDeleted(h hubInterface, roomID, msgID string) {
	h.BroadcastToRoom(roomID, "message_deleted", messageDeletedPayload{MessageID: msgID})
}

// BroadcastMessageReacted broadcast event "message_reacted" ไปทุก Client ในห้อง
func BroadcastMessageReacted(h hubInterface, roomID, msgID string, reactions map[string][]string) {
	if reactions == nil {
		reactions = make(map[string][]string)
	}
	h.BroadcastToRoom(roomID, "message_reacted", messageReactedPayload{MessageID: msgID, Reactions: reactions})
}

// BroadcastPinsUpdated broadcast event "pins_updated" ไปทุก Client ในห้อง
func BroadcastPinsUpdated(h hubInterface, roomID string, pins []model.ChatMessage) {
	if pins == nil {
		pins = []model.ChatMessage{}
	}
	h.BroadcastToRoom(roomID, "pins_updated", pinsUpdatedPayload{Pins: pins})
}

// MarshalWSMessage แปลง event + payload เป็น JSON bytes
func MarshalWSMessage(event string, payload interface{}) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(model.WSMessage{
		Event:   event,
		Payload: raw,
	})
}
