package broadcast

import (
	"context"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
)

// fakeSettingsStore — embed store.Store (nil) → method ที่ไม่ override panic ถ้าถูกเรียก
type fakeSettingsStore struct {
	store.Store
	allowSkip      bool
	reachedGetVote bool
	setVoteCalled  bool
}

func (f *fakeSettingsStore) GetSettings(_ context.Context) (*model.AppSettings, error) {
	return &model.AppSettings{AllowSkipBroadcast: f.allowSkip}, nil
}

func (f *fakeSettingsStore) GetVote(_ context.Context, _ string) (*model.Vote, error) {
	f.reachedGetVote = true // ถ้าถึงตรงนี้ = ผ่าน gate AllowSkipBroadcast มาแล้ว
	return nil, nil
}

func (f *fakeSettingsStore) SetVote(_ context.Context, _ string, _ *model.Vote, _ time.Duration) error {
	f.setVoteCalled = true
	return nil
}

// admin ปิด skip broadcast → scheduler ต้องไม่เปิด vote popup (เดิม bug: ขึ้นทุกครั้งที่ broadcast เริ่ม)
func TestBroadcastSkipVoteBlockedWhenDisabled(t *testing.T) {
	fs := &fakeSettingsStore{allowSkip: false}
	h := hub.NewHub(fs)

	startBroadcastSkipVote(h, fs, "100000")

	if fs.reachedGetVote {
		t.Fatalf("AllowSkipBroadcast=false: ต้องหยุดที่ gate ไม่ดำเนินการต่อ (skip popup ต้องไม่ขึ้น)")
	}
	if fs.setVoteCalled {
		t.Fatalf("AllowSkipBroadcast=false: ต้องไม่สร้าง vote")
	}
}

// admin เปิด skip broadcast → ผ่าน gate ไปทำงานต่อ (ถึงขั้นตอนเช็ค vote/online users)
func TestBroadcastSkipVotePassesGateWhenEnabled(t *testing.T) {
	fs := &fakeSettingsStore{allowSkip: true}
	h := hub.NewHub(fs)

	startBroadcastSkipVote(h, fs, "100000")

	if !fs.reachedGetVote {
		t.Fatalf("AllowSkipBroadcast=true: ต้องผ่าน gate ไปดำเนินการต่อ")
	}
}
