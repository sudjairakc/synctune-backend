package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/synctune/backend/model"
)

const (
	stateKey   = "synctune:state"
	historyKey = "synctune:history"
	chatKey    = "synctune:chat"
	maxHistory = 50
	maxChat    = 100
)

// Store กำหนด Interface สำหรับการเข้าถึง Storage
type Store interface {
	GetState(ctx context.Context) (*model.PlaylistState, error)
	SetState(ctx context.Context, state *model.PlaylistState) error
	PushHistory(ctx context.Context, song model.HistorySong) error
	GetHistory(ctx context.Context) ([]model.HistorySong, error)
	PushChatMessage(ctx context.Context, msg model.ChatMessage) error
	GetChatHistory(ctx context.Context) ([]model.ChatMessage, error)
	FlushAll(ctx context.Context) error
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
func (s *RedisStore) GetState(ctx context.Context) (*model.PlaylistState, error) {
	data, err := s.client.Get(ctx, stateKey).Bytes()
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
func (s *RedisStore) SetState(ctx context.Context, state *model.PlaylistState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("SetState: marshal: %w", err)
	}
	if err := s.client.Set(ctx, stateKey, data, 0).Err(); err != nil {
		return fmt.Errorf("SetState: redis SET: %w", err)
	}
	return nil
}

// PushHistory เพิ่ม HistorySong ลงใน History (LPUSH + LTRIM, Atomic Pipeline)
func (s *RedisStore) PushHistory(ctx context.Context, song model.HistorySong) error {
	data, err := json.Marshal(song)
	if err != nil {
		return fmt.Errorf("PushHistory: marshal: %w", err)
	}
	// ใช้ Pipeline เพื่อให้ LPUSH และ LTRIM เป็น Atomic
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, historyKey, data)
		pipe.LTrim(ctx, historyKey, 0, maxHistory-1)
		return nil
	})
	if err != nil {
		return fmt.Errorf("PushHistory: pipeline: %w", err)
	}
	return nil
}

// GetHistory ดึง History ทั้งหมดจาก Redis (newest first)
func (s *RedisStore) GetHistory(ctx context.Context) ([]model.HistorySong, error) {
	items, err := s.client.LRange(ctx, historyKey, 0, maxHistory-1).Result()
	if err != nil {
		return nil, fmt.Errorf("GetHistory: redis LRANGE: %w", err)
	}
	history := make([]model.HistorySong, 0, len(items))
	for _, item := range items {
		var song model.HistorySong
		if err := json.Unmarshal([]byte(item), &song); err != nil {
			continue // ข้าม item ที่ parse ไม่ได้
		}
		history = append(history, song)
	}
	return history, nil
}

// PushChatMessage เพิ่ม ChatMessage ลงใน chat history (LPUSH + LTRIM, newest first)
func (s *RedisStore) PushChatMessage(ctx context.Context, msg model.ChatMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("PushChatMessage: marshal: %w", err)
	}
	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, chatKey, data)
		pipe.LTrim(ctx, chatKey, 0, maxChat-1)
		return nil
	})
	if err != nil {
		return fmt.Errorf("PushChatMessage: pipeline: %w", err)
	}
	return nil
}

// GetChatHistory ดึง chat history จาก Redis (newest first)
func (s *RedisStore) GetChatHistory(ctx context.Context) ([]model.ChatMessage, error) {
	items, err := s.client.LRange(ctx, chatKey, 0, maxChat-1).Result()
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

// FlushAll ลบ keys ทั้งหมดของ synctune ออกจาก Redis
func (s *RedisStore) FlushAll(ctx context.Context) error {
	if err := s.client.Del(ctx, stateKey, historyKey, chatKey).Err(); err != nil {
		return fmt.Errorf("FlushAll: %w", err)
	}
	return nil
}
