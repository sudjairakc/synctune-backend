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

// roomSoundPadKey คืน Redis key สำหรับ sound pad ของห้องนั้น
func roomSoundPadKey(roomID string) string { return "synctune:room:" + roomID + ":soundpad" }

// roomSoundPadHistoryKey คืน Redis key สำหรับ soundpad play history ของห้องนั้น
func roomSoundPadHistoryKey(roomID string) string {
	return "synctune:room:" + roomID + ":soundpad_history"
}

// Store กำหนด Interface สำหรับการเข้าถึง Storage
type Store interface {
	GetState(ctx context.Context, roomID string) (*model.PlaylistState, error)
	SetState(ctx context.Context, roomID string, state *model.PlaylistState) error
	PushHistory(ctx context.Context, roomID string, song model.HistorySong) error
	GetHistory(ctx context.Context, roomID string) ([]model.HistorySong, error)
	PushChatMessage(ctx context.Context, roomID string, msg model.ChatMessage) error
	GetChatHistory(ctx context.Context, roomID string) ([]model.ChatMessage, error)
	GetSoundPad(ctx context.Context, roomID string) ([]*model.SoundPadSlot, error)
	SetSoundPad(ctx context.Context, roomID string, pad []*model.SoundPadSlot) error
	PushSoundPadPlay(ctx context.Context, roomID string, event model.SoundPadPlayEvent) error
	GetSoundPadHistory(ctx context.Context, roomID string) ([]model.SoundPadPlayEvent, error)
	DeleteRoom(ctx context.Context, roomID string) error
	FlushAll(ctx context.Context) error
	// ClaimSongEnded ใช้ SET NX เพื่อ dedup — คืน true ถ้า claim สำเร็จ (ประมวลผลได้)
	ClaimSongEnded(ctx context.Context, roomID, queueID string) (bool, error)
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
	return &state, nil
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
	if err := s.client.Del(ctx, roomStateKey(roomID), roomHistoryKey(roomID), roomChatKey(roomID), roomSoundPadKey(roomID), roomSoundPadHistoryKey(roomID)).Err(); err != nil {
		return fmt.Errorf("DeleteRoom: %w", err)
	}
	return nil
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
