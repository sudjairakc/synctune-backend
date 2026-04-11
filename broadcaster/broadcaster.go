// Package broadcaster มี Helper Functions สำหรับ Broadcast WebSocket Events
package broadcaster

import (
	"encoding/json"

	"github.com/olahol/melody"
	"github.com/synctune/backend/model"
)

// hubInterface ป้องกัน Circular import ระหว่าง hub และ broadcaster
type hubInterface interface {
	Broadcast(event string, payload interface{})
	SendToSession(session *melody.Session, event string, payload interface{})
}

// queueUpdatedPayload คือ Payload ของ event queue_updated
type queueUpdatedPayload struct {
	CurrentQueue []model.Song        `json:"current_queue"`
	CurrentIndex int                 `json:"current_index"`
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

// initialStatePayload คือ Payload ของ event initial_state
type initialStatePayload struct {
	CurrentQueue []model.Song         `json:"current_queue"`
	CurrentIndex int                  `json:"current_index"`
	SeekTime     int                  `json:"seek_time"`
	IsPlaying    bool                 `json:"is_playing"`
	Autoplay     bool                 `json:"autoplay"`
	Shuffle      bool                 `json:"shuffle"`
	RandomPlay   bool                 `json:"random_play"`
	History      []model.HistorySong  `json:"history"`
	ChatHistory  []model.ChatMessage  `json:"chat_history"`
	OnlineUsers  []model.User         `json:"online_users"`
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

// BroadcastQueueUpdated Broadcast event "queue_updated" ไปทุก Client
func BroadcastQueueUpdated(h hubInterface, state *model.PlaylistState, history []model.HistorySong) {
	h.Broadcast("queue_updated", queueUpdatedPayload{
		CurrentQueue: state.CurrentQueue,
		CurrentIndex: state.CurrentIndex,
		IsPlaying:    state.IsPlaying,
		History:      history,
	})
}

// BroadcastSeekSync Broadcast event "seek_sync" ไปทุก Client
func BroadcastSeekSync(h hubInterface, seekTime int, isPlaying bool) {
	h.Broadcast("seek_sync", seekSyncPayload{
		SeekTime:  seekTime,
		IsPlaying: isPlaying,
	})
}

// BroadcastSongSkipped Broadcast event "song_skipped" ไปทุก Client
// errorCode: 0 = user skip, 101/150 = YouTube embed error
func BroadcastSongSkipped(h hubInterface, song model.Song, errorCode int) {
	reason := "user_skipped"
	if errorCode == 101 {
		reason = "embed_not_allowed"
	} else if errorCode == 150 {
		reason = "embed_not_allowed_by_request"
	}
	h.Broadcast("song_skipped", songSkippedPayload{
		SongID:    song.ID,
		Title:     song.Title,
		Reason:    reason,
		ErrorCode: errorCode,
	})
}

// SendInitialState ส่ง event "initial_state" ไปยัง Client ใหม่เท่านั้น (ไม่ Broadcast)
func SendInitialState(h hubInterface, session *melody.Session, state *model.PlaylistState, history []model.HistorySong, chatHistory []model.ChatMessage, onlineUsers []model.User) {
	h.SendToSession(session, "initial_state", initialStatePayload{
		CurrentQueue: state.CurrentQueue,
		CurrentIndex: state.CurrentIndex,
		SeekTime:     state.SeekTime,
		IsPlaying:    state.IsPlaying,
		Autoplay:     state.Autoplay,
		Shuffle:      state.Shuffle,
		RandomPlay:   state.RandomPlay,
		History:      history,
		ChatHistory:  chatHistory,
		OnlineUsers:  onlineUsers,
	})
}

// BroadcastPlaybackModeUpdated Broadcast event "playback_mode_updated" ไปทุก Client
func BroadcastPlaybackModeUpdated(h hubInterface, state *model.PlaylistState) {
	h.Broadcast("playback_mode_updated", playbackModePayload{
		Autoplay:   state.Autoplay,
		Shuffle:    state.Shuffle,
		RandomPlay: state.RandomPlay,
	})
}

// BroadcastUserJoined Broadcast event "user_joined" ไปทุก Client
func BroadcastUserJoined(h hubInterface, user model.User, onlineUsers []model.User) {
	h.Broadcast("user_joined", userEventPayload{User: user, OnlineUsers: onlineUsers})
}

// BroadcastUserLeft Broadcast event "user_left" ไปทุก Client
func BroadcastUserLeft(h hubInterface, user model.User, onlineUsers []model.User) {
	h.Broadcast("user_left", userEventPayload{User: user, OnlineUsers: onlineUsers})
}

// BroadcastMessageReceived Broadcast event "message_received" ไปทุก Client
func BroadcastMessageReceived(h hubInterface, msg model.ChatMessage) {
	h.Broadcast("message_received", msg)
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
