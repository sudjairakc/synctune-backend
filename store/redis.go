package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/synctune/backend/model"
)

const (
	maxHistory = 50
	maxChat    = 100
)

// roomStateKey คืน Redis key สำหรับ state ของห้องนั้น
func roomStateKey(roomID string) string { return "synctune:room:" + roomID + ":state" }

// roomHistoryKey คืน Redis key สำหรับ history ของห้องนั้น
func roomHistoryKey(roomID string) string { return "synctune:room:" + roomID + ":history" }

// roomChatKey คืน Redis key สำหรับ chat ของห้องนั้น
func roomChatKey(roomID string) string { return "synctune:room:" + roomID + ":chat" }

// roomPinsKey คืน Redis key สำหรับ pinned messages ของห้องนั้น
func roomPinsKey(roomID string) string { return "synctune:room:" + roomID + ":pins" }

// roomSoundPadKey คืน Redis key สำหรับ sound pad ของห้องนั้น
func roomSoundPadKey(roomID string) string { return "synctune:room:" + roomID + ":soundpad" }

// roomSoundPadHistoryKey คืน Redis key สำหรับ soundpad play history ของห้องนั้น
func roomSoundPadHistoryKey(roomID string) string {
	return "synctune:room:" + roomID + ":soundpad_history"
}

// roomLastEmptiedKey คืน Redis key ที่เก็บ Unix timestamp ตอนห้องว่างล่าสุด
func roomLastEmptiedKey(roomID string) string { return "synctune:room:" + roomID + ":last_emptied" }

// Store กำหนด Interface สำหรับการเข้าถึง Storage
type Store interface {
	GetState(ctx context.Context, roomID string) (*model.PlaylistState, error)
	SetState(ctx context.Context, roomID string, state *model.PlaylistState) error
	PushHistory(ctx context.Context, roomID string, song model.HistorySong) error
	GetHistory(ctx context.Context, roomID string) ([]model.HistorySong, error)
	PushChatMessage(ctx context.Context, roomID string, msg model.ChatMessage) error
	GetChatHistory(ctx context.Context, roomID string) ([]model.ChatMessage, error)
	GetChatMessageByID(ctx context.Context, roomID, msgID string) (*model.ChatMessage, error)
	DeleteChatMessage(ctx context.Context, roomID, msgID string) error
	ToggleChatReaction(ctx context.Context, roomID, msgID, emoji, userID string) (map[string][]string, error)
	GetPinnedMessages(ctx context.Context, roomID string) ([]model.ChatMessage, error)
	TogglePinMessage(ctx context.Context, roomID string, msg model.ChatMessage) (bool, []model.ChatMessage, error)
	GetSoundPad(ctx context.Context, roomID string) ([]*model.SoundPadSlot, error)
	SetSoundPad(ctx context.Context, roomID string, pad []*model.SoundPadSlot) error
	PushSoundPadPlay(ctx context.Context, roomID string, event model.SoundPadPlayEvent) error
	GetSoundPadHistory(ctx context.Context, roomID string) ([]model.SoundPadPlayEvent, error)
	DeleteRoom(ctx context.Context, roomID string) error
	FlushAll(ctx context.Context) error
	// ClaimSongEnded ใช้ SET NX เพื่อ dedup — คืน true ถ้า claim สำเร็จ (ประมวลผลได้)
	ClaimSongEnded(ctx context.Context, roomID, queueID string) (bool, error)
	// SetRoomLastEmptied บันทึกเวลาที่ห้องว่าง (ไม่มี user) — ใช้คำนวณอายุห้องสำหรับ cleanup
	SetRoomLastEmptied(ctx context.Context, roomID string) error
	// ListRoomsLastEmptied คืน map roomID → เวลาที่ห้องว่างล่าสุด สำหรับ cleanup job
	ListRoomsLastEmptied(ctx context.Context) (map[string]time.Time, error)
	// GetSchedules คืน broadcast schedules ทั้งหมด
	GetSchedules(ctx context.Context) ([]model.BroadcastSchedule, error)
	// SetSchedules บันทึก broadcast schedules ลง Redis
	SetSchedules(ctx context.Context, schedules []model.BroadcastSchedule) error
	// IncrSeekTime เพิ่ม SeekTime แบบ atomic ด้วย Lua script
	// คืน seek_time ใหม่, หรือ -1 ถ้าห้องไม่ได้เล่นหรือเป็น live stream
	IncrSeekTime(ctx context.Context, roomID string, delta int) (int, error)
}

// RedisStore คือ Implementation ของ Store ที่ใช้ Redis
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore สร้าง RedisStore ใหม่และตรวจสอบการเชื่อมต่อ
// รับทั้ง "host:port" และ full URL "redis://[:password@]host:port[/db]"
func NewRedisStore(url string) (*RedisStore, error) {
	var opts *redis.Options
	if strings.HasPrefix(url, "redis://") || strings.HasPrefix(url, "rediss://") {
		var err error
		opts, err = redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("NewRedisStore: parse url: %w", err)
		}
	} else {
		opts = &redis.Options{Addr: url}
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("NewRedisStore: ping redis: %w", err)
	}
	return &RedisStore{client: client}, nil
}

// GetState โหลด PlaylistState จาก Redis
// คืน State ว่าง (Queue ว่าง, IsPlaying=false) ถ้ายังไม่มีข้อมูล
func (s *RedisStore) GetState(ctx context.Context, roomID string) (*model.PlaylistState, error) {
	data, err := s.client.Get(ctx, roomStateKey(roomID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return &model.PlaylistState{
				CurrentQueue: []model.Song{},
			}, nil
		}
		return nil, fmt.Errorf("GetState: redis GET: %w", err)
	}
	var state model.PlaylistState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("GetState: unmarshal: %w", err)
	}
	if state.CurrentQueue == nil {
		state.CurrentQueue = []model.Song{}
	}
	// backfill QueueID สำหรับ songs เก่าที่ไม่มี queue_id (migration guard)
	for i := range state.CurrentQueue {
		if state.CurrentQueue[i].QueueID == "" {
			state.CurrentQueue[i].QueueID = state.CurrentQueue[i].ID + fmt.Sprintf("_%d", i)
		}
	}
	normalizeSeekTimeForLiveTrack(&state)
	return &state, nil
}

// normalizeSeekTimeForLiveTrack วิดีโอสดไม่ควรใช้ seek_time จาก VoD — คืน 0 ใน state ที่โหลด (ไม่เขียน Redis ทันที)
func normalizeSeekTimeForLiveTrack(state *model.PlaylistState) {
	if len(state.CurrentQueue) == 0 {
		return
	}
	i := state.CurrentIndex
	if i < 0 || i >= len(state.CurrentQueue) {
		return
	}
	if state.CurrentQueue[i].IsLive {
		state.SeekTime = 0
	}
}

// SetState บันทึก PlaylistState ลง Redis
func (s *RedisStore) SetState(ctx context.Context, roomID string, state *model.PlaylistState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("SetState: marshal: %w", err)
	}
	if err := s.client.Set(ctx, roomStateKey(roomID), data, 0).Err(); err != nil {
		return fmt.Errorf("SetState: redis SET: %w", err)
	}
	return nil
}

// PushHistory เพิ่ม HistorySong ลงใน History (LPUSH + LTRIM, Atomic Pipeline)
func (s *RedisStore) PushHistory(ctx context.Context, roomID string, song model.HistorySong) error {
	data, err := json.Marshal(song)
	if err != nil {
		return fmt.Errorf("PushHistory: marshal: %w", err)
	}
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, roomHistoryKey(roomID), data)
		pipe.LTrim(ctx, roomHistoryKey(roomID), 0, maxHistory-1)
		return nil
	})
	if err != nil {
		return fmt.Errorf("PushHistory: pipeline: %w", err)
	}
	return nil
}

// GetHistory ดึง History ทั้งหมดจาก Redis (newest first)
func (s *RedisStore) GetHistory(ctx context.Context, roomID string) ([]model.HistorySong, error) {
	items, err := s.client.LRange(ctx, roomHistoryKey(roomID), 0, maxHistory-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetHistory: redis LRANGE: %w", err)
	}
	history := make([]model.HistorySong, 0, len(items))
	for _, item := range items {
		var song model.HistorySong
		if err := json.Unmarshal([]byte(item), &song); err != nil {
			continue
		}
		history = append(history, song)
	}
	return history, nil
}

// PushChatMessage เพิ่ม ChatMessage ลงใน chat history (LPUSH + LTRIM, newest first)
func (s *RedisStore) PushChatMessage(ctx context.Context, roomID string, msg model.ChatMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("PushChatMessage: marshal: %w", err)
	}
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, roomChatKey(roomID), data)
		pipe.LTrim(ctx, roomChatKey(roomID), 0, maxChat-1)
		return nil
	})
	if err != nil {
		return fmt.Errorf("PushChatMessage: pipeline: %w", err)
	}
	return nil
}

// GetChatHistory ดึง chat history จาก Redis (newest first)
func (s *RedisStore) GetChatHistory(ctx context.Context, roomID string) ([]model.ChatMessage, error) {
	items, err := s.client.LRange(ctx, roomChatKey(roomID), 0, maxChat-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetChatHistory: redis LRANGE: %w", err)
	}
	msgs := make([]model.ChatMessage, 0, len(items))
	for _, item := range items {
		var msg model.ChatMessage
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// updateChatMessage helper: LRANGE → find by msgID → mutate → LSET
func (s *RedisStore) updateChatMessage(ctx context.Context, roomID, msgID string, updater func(*model.ChatMessage)) error {
	key := roomChatKey(roomID)
	items, err := s.client.LRange(ctx, key, 0, maxChat-1).Result()
	if err != nil {
		return fmt.Errorf("updateChatMessage: LRANGE: %w", err)
	}
	for i, item := range items {
		var msg model.ChatMessage
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			continue
		}
		if msg.ID != msgID {
			continue
		}
		updater(&msg)
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("updateChatMessage: marshal: %w", err)
		}
		if err := s.client.LSet(ctx, key, int64(i), string(data)).Err(); err != nil {
			return fmt.Errorf("updateChatMessage: LSET: %w", err)
		}
		return nil
	}
	return fmt.Errorf("updateChatMessage: message not found: %s", msgID)
}

// GetChatMessageByID ดึง ChatMessage ตาม ID จาก chat history (nil ถ้าไม่พบ)
func (s *RedisStore) GetChatMessageByID(ctx context.Context, roomID, msgID string) (*model.ChatMessage, error) {
	items, err := s.client.LRange(ctx, roomChatKey(roomID), 0, maxChat-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetChatMessageByID: LRANGE: %w", err)
	}
	for _, item := range items {
		var msg model.ChatMessage
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			continue
		}
		if msg.ID == msgID {
			return &msg, nil
		}
	}
	return nil, nil
}

// DeleteChatMessage ทำเครื่องหมาย deleted=true และล้างเนื้อหาข้อความ
func (s *RedisStore) DeleteChatMessage(ctx context.Context, roomID, msgID string) error {
	return s.updateChatMessage(ctx, roomID, msgID, func(msg *model.ChatMessage) {
		msg.Deleted = true
		msg.Text = ""
		msg.ImageURL = ""
	})
}

// ToggleChatReaction toggle emoji reaction ของ user บนข้อความ คืน reactions map หลัง toggle
func (s *RedisStore) ToggleChatReaction(ctx context.Context, roomID, msgID, emoji, userID string) (map[string][]string, error) {
	var result map[string][]string
	err := s.updateChatMessage(ctx, roomID, msgID, func(msg *model.ChatMessage) {
		if msg.Reactions == nil {
			msg.Reactions = make(map[string][]string)
		}
		users := msg.Reactions[emoji]
		removed := false
		for i, uid := range users {
			if uid == userID {
				msg.Reactions[emoji] = append(users[:i], users[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			msg.Reactions[emoji] = append(users, userID)
		}
		if len(msg.Reactions[emoji]) == 0 {
			delete(msg.Reactions, emoji)
		}
		result = msg.Reactions
	})
	return result, err
}

const maxPins = 20

// GetPinnedMessages ดึง pinned messages ทั้งหมด (newest first)
func (s *RedisStore) GetPinnedMessages(ctx context.Context, roomID string) ([]model.ChatMessage, error) {
	items, err := s.client.LRange(ctx, roomPinsKey(roomID), 0, maxPins-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetPinnedMessages: LRANGE: %w", err)
	}
	msgs := make([]model.ChatMessage, 0, len(items))
	for _, item := range items {
		var msg model.ChatMessage
		if err := json.Unmarshal([]byte(item), &msg); err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// TogglePinMessage pin หรือ unpin ข้อความ คืน (pinned bool, updated pins list, error)
func (s *RedisStore) TogglePinMessage(ctx context.Context, roomID string, msg model.ChatMessage) (bool, []model.ChatMessage, error) {
	// ตรวจสอบว่า pin อยู่แล้วหรือไม่
	existing, err := s.GetPinnedMessages(ctx, roomID)
	if err != nil {
		return false, nil, err
	}
	for _, p := range existing {
		if p.ID == msg.ID {
			// unpin: ลบออก
			items, err := s.client.LRange(ctx, roomPinsKey(roomID), 0, maxPins-1).Result()
			if err != nil {
				return false, nil, fmt.Errorf("TogglePinMessage: LRANGE: %w", err)
			}
			for _, item := range items {
				var m model.ChatMessage
				if err := json.Unmarshal([]byte(item), &m); err != nil {
					continue
				}
				if m.ID == msg.ID {
					_ = s.client.LRem(ctx, roomPinsKey(roomID), 0, item).Err()
					break
				}
			}
			pins, _ := s.GetPinnedMessages(ctx, roomID)
			return false, pins, nil
		}
	}
	// pin: เพิ่มข้อมูล
	data, err := json.Marshal(msg)
	if err != nil {
		return false, nil, fmt.Errorf("TogglePinMessage: marshal: %w", err)
	}
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, roomPinsKey(roomID), data)
		pipe.LTrim(ctx, roomPinsKey(roomID), 0, maxPins-1)
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("TogglePinMessage: pipeline: %w", err)
	}
	pins, _ := s.GetPinnedMessages(ctx, roomID)
	return true, pins, nil
}

// GetSoundPad โหลด Sound Pad ([50]*SoundPadSlot) จาก Redis
func (s *RedisStore) GetSoundPad(ctx context.Context, roomID string) ([]*model.SoundPadSlot, error) {
	pad := make([]*model.SoundPadSlot, model.SoundPadSize)
	val, err := s.client.Get(ctx, roomSoundPadKey(roomID)).Result()
	if errors.Is(err, redis.Nil) {
		return pad, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetSoundPad: redis GET: %w", err)
	}
	if err := json.Unmarshal([]byte(val), &pad); err != nil {
		return nil, fmt.Errorf("GetSoundPad: unmarshal: %w", err)
	}
	return pad, nil
}

// SetSoundPad บันทึก Sound Pad ลง Redis
func (s *RedisStore) SetSoundPad(ctx context.Context, roomID string, pad []*model.SoundPadSlot) error {
	data, err := json.Marshal(pad)
	if err != nil {
		return fmt.Errorf("SetSoundPad: marshal: %w", err)
	}
	if err := s.client.Set(ctx, roomSoundPadKey(roomID), data, 0).Err(); err != nil {
		return fmt.Errorf("SetSoundPad: redis SET: %w", err)
	}
	return nil
}

const maxSoundPadHistory = 100

// PushSoundPadPlay บันทึก soundpad play event ลง history (LPUSH + LTRIM, newest first)
func (s *RedisStore) PushSoundPadPlay(ctx context.Context, roomID string, event model.SoundPadPlayEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("PushSoundPadPlay: marshal: %w", err)
	}
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, roomSoundPadHistoryKey(roomID), data)
		pipe.LTrim(ctx, roomSoundPadHistoryKey(roomID), 0, maxSoundPadHistory-1)
		return nil
	})
	if err != nil {
		return fmt.Errorf("PushSoundPadPlay: pipeline: %w", err)
	}
	return nil
}

// GetSoundPadHistory ดึง soundpad play history จาก Redis (newest first)
func (s *RedisStore) GetSoundPadHistory(ctx context.Context, roomID string) ([]model.SoundPadPlayEvent, error) {
	items, err := s.client.LRange(ctx, roomSoundPadHistoryKey(roomID), 0, maxSoundPadHistory-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetSoundPadHistory: redis LRANGE: %w", err)
	}
	events := make([]model.SoundPadPlayEvent, 0, len(items))
	for _, item := range items {
		var e model.SoundPadPlayEvent
		if err := json.Unmarshal([]byte(item), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// DeleteRoom ลบ Redis keys ทั้งหมดของห้องนั้น
func (s *RedisStore) DeleteRoom(ctx context.Context, roomID string) error {
	keys := []string{
		roomStateKey(roomID),
		roomHistoryKey(roomID),
		roomChatKey(roomID),
		roomPinsKey(roomID),
		roomSoundPadKey(roomID),
		roomSoundPadHistoryKey(roomID),
		roomLastEmptiedKey(roomID),
	}
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("DeleteRoom: %w", err)
	}
	return nil
}

// SetRoomLastEmptied บันทึก Unix timestamp ปัจจุบันเป็น "เวลาที่ห้องว่าง" สำหรับ cleanup job
func (s *RedisStore) SetRoomLastEmptied(ctx context.Context, roomID string) error {
	if err := s.client.Set(ctx, roomLastEmptiedKey(roomID), time.Now().Unix(), 0).Err(); err != nil {
		return fmt.Errorf("SetRoomLastEmptied: %w", err)
	}
	return nil
}

// ListRoomsLastEmptied สแกน Redis หา rooms ที่มี last_emptied key และคืน map roomID → เวลา
func (s *RedisStore) ListRoomsLastEmptied(ctx context.Context) (map[string]time.Time, error) {
	result := make(map[string]time.Time)
	var cursor uint64
	for {
		keys, next, err := s.client.Scan(ctx, cursor, "synctune:room:*:last_emptied", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("ListRoomsLastEmptied: scan: %w", err)
		}
		for _, key := range keys {
			// key format: synctune:room:{roomID}:last_emptied
			parts := strings.SplitN(key, ":", 4)
			if len(parts) != 4 {
				continue
			}
			roomID := parts[2]
			ts, err := s.client.Get(ctx, key).Int64()
			if err != nil {
				continue
			}
			result[roomID] = time.Unix(ts, 0)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return result, nil
}

// ClaimSongEnded ใช้ SET NX เพื่อ dedup song_ended per queue_id
// คืน true ถ้า claim สำเร็จ (client นี้เป็นคนแรก) — คืน false ถ้ามี client อื่น claim ไปก่อนแล้ว
func (s *RedisStore) ClaimSongEnded(ctx context.Context, roomID, queueID string) (bool, error) {
	key := "synctune:room:" + roomID + ":song_ended:" + queueID
	ok, err := s.client.SetNX(ctx, key, 1, 60*time.Second).Result()
	if err != nil {
		return false, fmt.Errorf("ClaimSongEnded: %w", err)
	}
	return ok, nil
}

// FlushAll ลบ keys ทั้งหมดของ synctune ออกจาก Redis (SCAN-based)
func (s *RedisStore) FlushAll(ctx context.Context) error {
	var cursor uint64
	var keys []string
	for {
		var batch []string
		var err error
		batch, cursor, err = s.client.Scan(ctx, cursor, "synctune:room:*", 100).Result()
		if err != nil {
			return fmt.Errorf("FlushAll: scan: %w", err)
		}
		keys = append(keys, batch...)
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("FlushAll: del: %w", err)
	}
	return nil
}

// incrSeekScript เพิ่ม seek_time แบบ atomic ใน Redis ด้วย Lua
// ป้องกัน race condition กับ triggerInRoom ที่ reset seek_time=0
var incrSeekScript = redis.NewScript(`
local data = redis.call('GET', KEYS[1])
if not data then return -1 end
local state = cjson.decode(data)
if not state.is_playing then return -1 end
local idx = (state.current_index or 0) + 1
local queue = state.current_queue or {}
if queue[idx] and queue[idx].is_live then return -1 end
local seek = (state.seek_time or 0) + tonumber(ARGV[1])
state.seek_time = seek
redis.call('SET', KEYS[1], cjson.encode(state))
return seek
`)

// IncrSeekTime เพิ่ม seek_time แบบ atomic — คืน -1 ถ้าห้องไม่ได้เล่นหรือเป็น live stream
func (s *RedisStore) IncrSeekTime(ctx context.Context, roomID string, delta int) (int, error) {
	result, err := incrSeekScript.Run(ctx, s.client, []string{roomStateKey(roomID)}, delta).Int()
	if err != nil {
		return 0, fmt.Errorf("IncrSeekTime: %w", err)
	}
	return result, nil
}

const broadcastSchedulesKey = "synctune:broadcast:schedules"

// GetSchedules คืน broadcast schedules ทั้งหมดจาก Redis
func (s *RedisStore) GetSchedules(ctx context.Context) ([]model.BroadcastSchedule, error) {
	data, err := s.client.Get(ctx, broadcastSchedulesKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []model.BroadcastSchedule{}, nil
		}
		return nil, fmt.Errorf("GetSchedules: redis GET: %w", err)
	}
	var schedules []model.BroadcastSchedule
	if err := json.Unmarshal(data, &schedules); err != nil {
		return nil, fmt.Errorf("GetSchedules: unmarshal: %w", err)
	}
	return schedules, nil
}

// SetSchedules บันทึก broadcast schedules ลง Redis
func (s *RedisStore) SetSchedules(ctx context.Context, schedules []model.BroadcastSchedule) error {
	data, err := json.Marshal(schedules)
	if err != nil {
		return fmt.Errorf("SetSchedules: marshal: %w", err)
	}
	if err := s.client.Set(ctx, broadcastSchedulesKey, data, 0).Err(); err != nil {
		return fmt.Errorf("SetSchedules: redis SET: %w", err)
	}
	return nil
}
