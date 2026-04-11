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
	Broadcast(event string, payload interface{})
	SendToSession(session *melody.Session, event string, payload interface{})
}

// SeekTicker Broadcast seek_sync ไปทุก Client ทุก interval วินาที
type SeekTicker struct {
	interval time.Duration
	hub      hubInterface
	store    store.Store
	stopCh   chan struct{}
}

// NewSeekTicker สร้าง SeekTicker ใหม่
// interval คือช่วงเวลาระหว่าง Broadcast (เช่น 5 * time.Second)
func NewSeekTicker(interval time.Duration, h hubInterface, s store.Store) *SeekTicker {
	return &SeekTicker{
		interval: interval,
		hub:      h,
		store:    s,
		stopCh:   make(chan struct{}),
	}
}

// Start เริ่ม Goroutine — เรียกครั้งเดียวตอน Startup
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
	state, err := t.store.GetState(ctx)
	if err != nil {
		log.Error().Err(err).Msg("SeekTicker: failed to get state")
		return
	}

	if !state.IsPlaying {
		return
	}

	state.SeekTime += int(t.interval.Seconds())

	if err := t.store.SetState(ctx, state); err != nil {
		log.Error().Err(err).Msg("SeekTicker: failed to set state")
		return
	}

	broadcaster.BroadcastSeekSync(t.hub, state.SeekTime, state.IsPlaying)
}
