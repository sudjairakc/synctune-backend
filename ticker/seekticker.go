// Package ticker มี Goroutine ที่ Broadcast seek_sync ทุก N วินาที
package ticker

import (
	"context"
	"time"

	"github.com/olahol/melody"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/store"
)

// hubInterface ป้องกัน Circular Import
type hubInterface interface {
	BroadcastToRoom(roomID string, event string, payload interface{})
	SendToSession(session *melody.Session, event string, payload interface{})
	ActiveRooms() []string
}

// SeekTicker Broadcast seek_sync ไปทุก Client ในทุกห้องที่กำลังเล่นอยู่ ทุก interval วินาที
type SeekTicker struct {
	interval time.Duration
	hub      hubInterface
	store    store.Store
	stopCh   chan struct{}
}

// NewSeekTicker สร้าง SeekTicker ใหม่
func NewSeekTicker(interval time.Duration, h hubInterface, s store.Store) *SeekTicker {
	return &SeekTicker{
		interval: interval,
		hub:      h,
		store:    s,
		stopCh:   make(chan struct{}),
	}
}

// Start เริ่ม Goroutine
func (t *SeekTicker) Start() {
	go t.run()
}

// Stop หยุด Goroutine อย่าง Graceful
func (t *SeekTicker) Stop() {
	close(t.stopCh)
}

func (t *SeekTicker) run() {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.tick()
		case <-t.stopCh:
			return
		}
	}
}

func (t *SeekTicker) tick() {
	ctx := context.Background()
	for _, roomID := range t.hub.ActiveRooms() {
		newSeek, err := t.store.IncrSeekTime(ctx, roomID, int(t.interval.Seconds()))
		if err != nil {
			log.Error().Err(err).Str("room_id", roomID).Msg("SeekTicker: failed to incr seek time")
			continue
		}
		if newSeek < 0 {
			continue
		}
		broadcaster.BroadcastSeekSync(t.hub, roomID, newSeek, true)
	}
}
