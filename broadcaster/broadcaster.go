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
	RoomID              string                    `json:"room_id"`
	CurrentQueue        []model.Song              `json:"current_queue"`
	CurrentIndex        int                       `json:"current_index"`
	SeekTime            int                       `json:"seek_time"`
	IsPlaying           bool                      `json:"is_playing"`
	Autoplay            bool                      `json:"autoplay"`
	Shuffle             bool                      `json:"shuffle"`
	RandomPlay          bool                      `json:"random_play"`
	History             []model.HistorySong       `json:"history"`
	ChatHistory         []model.ChatMessage       `json:"chat_history"`
	OnlineUsers         []model.User              `json:"online_users"`
	SoundPad            []*model.SoundPadSlot     `json:"sound_pad"`
	SoundPadHistory     []model.SoundPadPlayEvent `json:"soundpad_history"`
	PlaybackSpeed       float64                   `json:"playback_speed"`
	AllowSkipBroadcast  bool                      `json:"allow_skip_broadcast"`
	Presence            []model.Presence          `json:"presence_state"`
	ChannelHistories    map[string][]model.ChatMessage `json:"channel_histories,omitempty"`
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
func SendRoomJoined(h hubInterface, session *melody.Session, roomID string, state *model.PlaylistState, history []model.HistorySong, chatHistory []model.ChatMessage, onlineUsers []model.User, soundPad []*model.SoundPadSlot, soundPadHistory []model.SoundPadPlayEvent, presence []model.Presence, allowSkipBroadcast bool, channelHistories map[string][]model.ChatMessage) {
	speed := state.PlaybackSpeed
	if speed == 0 {
		speed = 1
	}
	if presence == nil {
		presence = []model.Presence{}
	}
	if channelHistories == nil {
		channelHistories = map[string][]model.ChatMessage{}
	}
	h.SendToSession(session, "room_joined", roomJoinedPayload{
		RoomID:             roomID,
		CurrentQueue:       state.CurrentQueue,
		CurrentIndex:       state.CurrentIndex,
		SeekTime:           state.SeekTime,
		IsPlaying:          state.IsPlaying,
		Autoplay:           state.Autoplay,
		Shuffle:            state.Shuffle,
		RandomPlay:         state.RandomPlay,
		History:            history,
		ChatHistory:        chatHistory,
		OnlineUsers:        onlineUsers,
		SoundPad:           soundPad,
		SoundPadHistory:    soundPadHistory,
		PlaybackSpeed:      speed,
		AllowSkipBroadcast: allowSkipBroadcast,
		Presence:           presence,
		ChannelHistories:   channelHistories,
	})
}

// presenceLeavePayload คือ payload ของ event presence_leave
type presenceLeavePayload struct {
	ConnectionID string `json:"connection_id"`
	UserID       string `json:"user_id"`
}

// zoneChangedPayload คือ payload ของ event zone_changed
type zoneChangedPayload struct {
	ConnectionID string `json:"connection_id"`
	UserID       string `json:"user_id"`
	ZoneID       string `json:"zone_id"`
}

// presenceCorrectedPayload คือ payload ของ event presence_corrected
type presenceCorrectedPayload struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Dir    string  `json:"dir"`
	ZoneID string  `json:"zone_id"`
}

// followUpdatedPayload คือ payload ของ event follow_updated
type followUpdatedPayload struct {
	ConnectionID string `json:"connection_id"`
	FollowingID  string `json:"following_id"`
}

// BroadcastPresenceUpdate broadcast event "presence_update" ไปทั้งห้อง
func BroadcastPresenceUpdate(h hubInterface, roomID string, p model.Presence) {
	h.BroadcastToRoom(roomID, "presence_update", p)
}

// BroadcastFollowUpdated broadcast event "follow_updated" ไปทั้งห้อง
func BroadcastFollowUpdated(h hubInterface, roomID, connectionID, followingID string) {
	h.BroadcastToRoom(roomID, "follow_updated", followUpdatedPayload{
		ConnectionID: connectionID,
		FollowingID:  followingID,
	})
}

// bubbleUpdatedPayload คือ payload ของ event bubble_updated
type bubbleUpdatedPayload struct {
	BubbleID string   `json:"bubble_id"`
	Members  []string `json:"members"`
}

// BroadcastBubbleUpdated broadcast event "bubble_updated" ไปทั้งห้อง
func BroadcastBubbleUpdated(h hubInterface, roomID, bubbleID string, members []string) {
	if members == nil {
		members = []string{}
	}
	h.BroadcastToRoom(roomID, "bubble_updated", bubbleUpdatedPayload{
		BubbleID: bubbleID,
		Members:  members,
	})
}

// BroadcastPresenceLeave broadcast event "presence_leave" ไปทั้งห้อง
func BroadcastPresenceLeave(h hubInterface, roomID, connectionID, userID string) {
	h.BroadcastToRoom(roomID, "presence_leave", presenceLeavePayload{
		ConnectionID: connectionID,
		UserID:       userID,
	})
}

// BroadcastZoneChanged broadcast event "zone_changed" ไปทั้งห้อง
func BroadcastZoneChanged(h hubInterface, roomID, connectionID, userID, zoneID string) {
	h.BroadcastToRoom(roomID, "zone_changed", zoneChangedPayload{
		ConnectionID: connectionID,
		UserID:       userID,
		ZoneID:       zoneID,
	})
}

// SendPresenceCorrected ส่ง event "presence_corrected" ไปยังผู้ส่ง
func SendPresenceCorrected(h hubInterface, session *melody.Session, x, y float64, dir, zoneID string) {
	h.SendToSession(session, "presence_corrected", presenceCorrectedPayload{
		X: x, Y: y, Dir: dir, ZoneID: zoneID,
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

// BroadcastSoundPadStop broadcast stop ไปทั้งห้อง
func BroadcastSoundPadStop(h hubInterface, roomID string) {
	h.BroadcastToRoom(roomID, "soundpad_stop", struct{}{})
}

// roomActionPayload คือ payload ของ event room_action
type roomActionPayload struct {
	Action     string `json:"action"`
	ByUsername string `json:"by_username"`
	Detail     string `json:"detail,omitempty"`
}

// BroadcastRoomAction broadcast action log ไปทั้งห้อง
func BroadcastRoomAction(h hubInterface, roomID, action, byUsername, detail string) {
	h.BroadcastToRoom(roomID, "room_action", roomActionPayload{Action: action, ByUsername: byUsername, Detail: detail})
}

// voteEventPayload คือ payload สำหรับ vote events ทุกประเภท
type voteEventPayload struct {
	VoteID      string `json:"vote_id"`
	Action      string `json:"action,omitempty"`
	SongQueueID string `json:"song_queue_id,omitempty"`
	SongTitle   string `json:"song_title,omitempty"`
	InitiatedBy string `json:"initiated_by,omitempty"`
	YesVotes    int    `json:"yes_votes"`
	Required    int    `json:"required"`
	Total       int    `json:"total,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
	Result      string `json:"result,omitempty"` // "passed" | "expired"
}

// BroadcastVoteStarted broadcast vote เริ่มใหม่ไปทั้งห้อง
func BroadcastVoteStarted(h hubInterface, roomID string, v *model.Vote) {
	h.BroadcastToRoom(roomID, "vote_started", voteEventPayload{
		VoteID:      v.ID,
		Action:      string(v.Action),
		SongQueueID: v.SongQueueID,
		SongTitle:   v.SongTitle,
		InitiatedBy: v.InitiatedBy,
		YesVotes:    len(v.YesVoterIDs),
		Required:    v.Required(),
		Total:       v.TotalAtStart,
		ExpiresAt:   v.ExpiresAt,
	})
}

// BroadcastVoteUpdated broadcast สถานะ vote หลังมีคนโหวต
func BroadcastVoteUpdated(h hubInterface, roomID string, v *model.Vote) {
	h.BroadcastToRoom(roomID, "vote_updated", voteEventPayload{
		VoteID:   v.ID,
		YesVotes: len(v.YesVoterIDs),
		Required: v.Required(),
	})
}

// BroadcastVoteResolved broadcast ผลโหวตสุดท้าย (passed / expired)
func BroadcastVoteResolved(h hubInterface, roomID string, v *model.Vote, result string) {
	h.BroadcastToRoom(roomID, "vote_resolved", voteEventPayload{
		VoteID: v.ID,
		Result: result,
	})
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
