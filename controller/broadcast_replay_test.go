package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
)

// fakeStore — in-memory Store สำหรับ test
// embed store.Store (nil) → method ที่ไม่ override จะ panic ถ้าถูกเรียก (จับ path ที่ไม่คาดคิด)
type fakeStore struct {
	store.Store
	state   map[string][]byte // roomID → marshaled PlaylistState (จำลอง Redis ที่ serialize ทุกครั้ง)
	history map[string][]model.HistorySong
	vote    map[string]*model.Vote
	claims  map[string]bool // "roomID:queueID" → claimed (จำลอง SET NX)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		state:   map[string][]byte{},
		history: map[string][]model.HistorySong{},
		vote:    map[string]*model.Vote{},
		claims:  map[string]bool{},
	}
}

func (f *fakeStore) GetState(_ context.Context, roomID string) (*model.PlaylistState, error) {
	b, ok := f.state[roomID]
	if !ok {
		return &model.PlaylistState{}, nil
	}
	var s model.PlaylistState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (f *fakeStore) SetState(_ context.Context, roomID string, s *model.PlaylistState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f.state[roomID] = b
	return nil
}

func (f *fakeStore) PushHistory(_ context.Context, roomID string, song model.HistorySong) error {
	f.history[roomID] = append([]model.HistorySong{song}, f.history[roomID]...)
	return nil
}

func (f *fakeStore) GetHistory(_ context.Context, roomID string) ([]model.HistorySong, error) {
	return f.history[roomID], nil
}

func (f *fakeStore) ClaimSongEnded(_ context.Context, roomID, queueID string) (bool, error) {
	key := roomID + ":" + queueID
	if f.claims[key] {
		return false, nil // มี claim อยู่แล้ว (จำลอง SET NX fail)
	}
	f.claims[key] = true
	return true, nil
}

func (f *fakeStore) GetVote(_ context.Context, roomID string) (*model.Vote, error) {
	return f.vote[roomID], nil
}

func (f *fakeStore) DeleteVote(_ context.Context, roomID string) error {
	delete(f.vote, roomID)
	return nil
}

func songEnded(t *testing.T, h *hub.Hub, client *hub.Client, queueID string) {
	t.Helper()
	payload, _ := json.Marshal(songEndedPayload{SongID: queueID})
	HandleSongEnded(h, client, payload)
}

// backdate ตั้ง BroadcastPlaybackStartedUnix ให้ผ่าน guard (since >= minBroadcastWallBeforeSongEndedSec)
func backdate(t *testing.T, fs *fakeStore, roomID string) {
	t.Helper()
	st, _ := fs.GetState(context.Background(), roomID)
	st.BroadcastPlaybackStartedUnix = time.Now().Unix() - 100
	_ = fs.SetState(context.Background(), roomID, st)
}

func TestBroadcastReplayThenRestore(t *testing.T) {
	const roomID = "100000"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	client := &hub.Client{ID: "c1", RoomID: roomID, User: model.User{ID: "c1", Username: "u1"}}

	// state เริ่มต้น: กำลังเล่น broadcast เพลง B, มีเพลงปกติ N ที่ saved ไว้รอ restore
	fs.state[roomID], _ = json.Marshal(&model.PlaylistState{
		CurrentQueue:                 []model.Song{{QueueID: "B", ID: "bid", IsBroadcast: true}},
		CurrentIndex:                 0,
		IsPlaying:                    true,
		IsBroadcasting:               true,
		BroadcastQueue:               nil,
		BroadcastVoteReplayDone:      false,
		SavedQueue:                   []model.Song{{QueueID: "N", ID: "nid"}},
		SavedCurrentIndex:            0,
		SavedIsPlaying:               true,
		BroadcastPlaybackStartedUnix: time.Now().Unix() - 100,
	})

	// รอบ 1: เพลง broadcast จบ + vote ไม่ผ่าน → ต้อง replay (locked) ยังเล่น B ต่อ
	songEnded(t, h, client, "B")

	st, _ := fs.GetState(context.Background(), roomID)
	if !st.BroadcastVoteReplayDone {
		t.Fatalf("รอบ 1: ควรตั้ง BroadcastVoteReplayDone=true (เข้า replay)")
	}
	if !st.IsBroadcasting {
		t.Fatalf("รอบ 1: ยังต้อง broadcasting (replay)")
	}
	// queue_id ต้องเปลี่ยน (เพิ่ม _replay) เพื่อให้ client reload + ส่ง song_ended รอบ replay ได้
	if len(st.CurrentQueue) != 1 || st.CurrentQueue[0].QueueID == "B" {
		t.Fatalf("รอบ 1: queue_id ต้องเปลี่ยนเป็นเพลง replay (ไม่ใช่ B เดิม), ได้ %+v", st.CurrentQueue)
	}
	if !st.CurrentQueue[0].IsBroadcast {
		t.Fatalf("รอบ 1: เพลง replay ต้องยังเป็น broadcast")
	}

	// รอบ 2: replay จบ → client ส่ง song_ended ด้วย queue_id ใหม่ → ต้อง restore กลับเพลงปกติ N (จุดที่เดิมค้าง)
	replayQueueID := st.CurrentQueue[0].QueueID
	backdate(t, fs, roomID)
	songEnded(t, h, client, replayQueueID)

	st, _ = fs.GetState(context.Background(), roomID)
	if st.IsBroadcasting {
		t.Fatalf("รอบ 2: replay จบแล้วต้องหยุด broadcasting (restore) — ยังค้างที่ broadcast = bug")
	}
	if len(st.CurrentQueue) != 1 || st.CurrentQueue[0].QueueID != "N" {
		t.Fatalf("รอบ 2: ควร restore กลับเพลงปกติ N, ได้ %+v", st.CurrentQueue)
	}
}

func TestBroadcastEndClearsActiveVote(t *testing.T) {
	const roomID = "100000"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	client := &hub.Client{ID: "c1", RoomID: roomID, User: model.User{ID: "c1", Username: "u1"}}

	// vote skip broadcast ค้างอยู่
	fs.vote[roomID] = &model.Vote{ID: "v1", Action: model.VoteActionSkipBroadcast, SongQueueID: "B"}

	// broadcast (replay เสร็จแล้ว) จบ → restore
	fs.state[roomID], _ = json.Marshal(&model.PlaylistState{
		CurrentQueue:                 []model.Song{{QueueID: "B", ID: "bid", IsBroadcast: true}},
		IsBroadcasting:               true,
		BroadcastVoteReplayDone:      true, // replay done → เข้า restore branch
		SavedQueue:                   []model.Song{{QueueID: "N", ID: "nid"}},
		BroadcastPlaybackStartedUnix: time.Now().Unix() - 100,
	})

	songEnded(t, h, client, "B")

	if fs.vote[roomID] != nil {
		t.Fatalf("vote ที่ค้างต้องถูก clear เมื่อ broadcast จบ (ปิด modal) — ยังค้าง = bug")
	}
}
