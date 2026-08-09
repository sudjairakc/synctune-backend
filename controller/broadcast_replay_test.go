package controller

import (
	"context"
	"encoding/json"
	"strings"
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
	state      map[string][]byte // roomID → marshaled PlaylistState (จำลอง Redis ที่ serialize ทุกครั้ง)
	history    map[string][]model.HistorySong
	vote       map[string]*model.Vote
	claims     map[string]bool // "roomID:queueID" → claimed (จำลอง SET NX)
	bellClaims map[string]bool // "roomID:from:to" → claimed (จำลอง bell SET NX)
	settings   model.AppSettings
	presence   map[string]map[string]model.Presence // roomID → connectionID → Presence
	channelMsg map[string][]model.ChatMessage       // "roomID\x00channel" → messages (newest first)
	private    map[string]*model.PrivateZoneState   // "roomID\x00zoneID" → state
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		state:      map[string][]byte{},
		history:    map[string][]model.HistorySong{},
		vote:       map[string]*model.Vote{},
		claims:     map[string]bool{},
		bellClaims: map[string]bool{},
		settings:   model.AppSettings{AllowSkipBroadcast: true}, // default: replay feature เปิด
		presence:   map[string]map[string]model.Presence{},
		channelMsg: map[string][]model.ChatMessage{},
		private:    map[string]*model.PrivateZoneState{},
	}
}

func (f *fakeStore) GetSettings(_ context.Context) (*model.AppSettings, error) {
	s := f.settings
	return &s, nil
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

func (f *fakeStore) TryClaimBell(_ context.Context, roomID, fromConnectionID, toConnectionID string, _ time.Duration) (bool, error) {
	key := roomID + ":" + fromConnectionID + ":" + toConnectionID
	if f.bellClaims[key] {
		return false, nil
	}
	f.bellClaims[key] = true
	return true, nil
}

func (f *fakeStore) GetVote(_ context.Context, roomID string) (*model.Vote, error) {
	return f.vote[roomID], nil
}

func (f *fakeStore) DeleteVote(_ context.Context, roomID string) error {
	delete(f.vote, roomID)
	return nil
}

func (f *fakeStore) GetChatHistory(_ context.Context, _ string) ([]model.ChatMessage, error) {
	return nil, nil
}

func (f *fakeStore) PushChannelMessage(_ context.Context, roomID, channel string, msg model.ChatMessage) error {
	key := roomID + "\x00" + channel
	f.channelMsg[key] = append([]model.ChatMessage{msg}, f.channelMsg[key]...)
	return nil
}

func (f *fakeStore) GetChannelHistory(_ context.Context, roomID, channel string, limit int) ([]model.ChatMessage, error) {
	key := roomID + "\x00" + channel
	msgs := f.channelMsg[key]
	if limit <= 0 || limit > len(msgs) {
		limit = len(msgs)
	}
	out := make([]model.ChatMessage, limit)
	copy(out, msgs[:limit])
	return out, nil
}

func (f *fakeStore) GetSoundPad(_ context.Context, _ string) ([]*model.SoundPadSlot, error) {
	return make([]*model.SoundPadSlot, model.SoundPadSize), nil
}

func (f *fakeStore) GetSoundPadHistory(_ context.Context, _ string) ([]model.SoundPadPlayEvent, error) {
	return nil, nil
}

func (f *fakeStore) GetPinnedMessages(_ context.Context, _ string) ([]model.ChatMessage, error) {
	return nil, nil
}

func (f *fakeStore) SetPresence(_ context.Context, roomID string, p model.Presence) error {
	if f.presence[roomID] == nil {
		f.presence[roomID] = map[string]model.Presence{}
	}
	f.presence[roomID][p.ConnectionID] = p
	return nil
}

func (f *fakeStore) GetAllPresence(_ context.Context, roomID string) ([]model.Presence, error) {
	m := f.presence[roomID]
	out := make([]model.Presence, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeStore) DeletePresence(_ context.Context, roomID, connectionID string) error {
	if m := f.presence[roomID]; m != nil {
		delete(m, connectionID)
	}
	return nil
}

func privateKey(roomID, zoneID string) string { return roomID + "\x00" + zoneID }

func (f *fakeStore) GetPrivateZoneState(_ context.Context, roomID, zoneID string) (*model.PrivateZoneState, error) {
	st := f.private[privateKey(roomID, zoneID)]
	if st == nil {
		return &model.PrivateZoneState{}, nil
	}
	out := &model.PrivateZoneState{
		Occupants: append([]string(nil), st.Occupants...),
		Invites:   append([]string(nil), st.Invites...),
	}
	return out, nil
}

func (f *fakeStore) SetPrivateZoneState(_ context.Context, roomID, zoneID string, state *model.PrivateZoneState) error {
	if state == nil {
		state = &model.PrivateZoneState{}
	}
	f.private[privateKey(roomID, zoneID)] = &model.PrivateZoneState{
		Occupants: append([]string(nil), state.Occupants...),
		Invites:   append([]string(nil), state.Invites...),
	}
	return nil
}

func (f *fakeStore) DeletePrivateZoneState(_ context.Context, roomID, zoneID string) error {
	delete(f.private, privateKey(roomID, zoneID))
	return nil
}

func (f *fakeStore) RemoveConnectionFromPrivateZones(_ context.Context, roomID, connectionID string) error {
	prefix := roomID + "\x00"
	for key, st := range f.private {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		st.Occupants = filterFakeID(st.Occupants, connectionID)
		st.Invites = filterFakeID(st.Invites, connectionID)
		if len(st.Occupants) == 0 {
			delete(f.private, key)
		}
	}
	return nil
}

func filterFakeID(ids []string, drop string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != drop {
			out = append(out, id)
		}
	}
	return out
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

func TestBroadcastNoReplayWhenSkipDisabled(t *testing.T) {
	const roomID = "100000"
	fs := newFakeStore()
	fs.settings.AllowSkipBroadcast = false // admin ปิด feature → ไม่มี incentive ให้ replay
	h := hub.NewHub(fs)
	client := &hub.Client{ID: "c1", RoomID: roomID, User: model.User{ID: "c1", Username: "u1"}}

	// broadcast กำลังเล่นรอบแรก (ยังไม่ replay) มีเพลงปกติ N รอ restore
	fs.state[roomID], _ = json.Marshal(&model.PlaylistState{
		CurrentQueue:                 []model.Song{{QueueID: "B", ID: "bid", IsBroadcast: true}},
		CurrentIndex:                 0,
		IsPlaying:                    true,
		IsBroadcasting:               true,
		BroadcastVoteReplayDone:      false,
		SavedQueue:                   []model.Song{{QueueID: "N", ID: "nid"}},
		SavedCurrentIndex:            0,
		SavedIsPlaying:               true,
		BroadcastPlaybackStartedUnix: time.Now().Unix() - 100,
	})

	// เพลง broadcast จบ → skip ปิดอยู่ → ต้อง restore ทันที (ไม่ replay)
	songEnded(t, h, client, "B")

	st, _ := fs.GetState(context.Background(), roomID)
	if st.IsBroadcasting {
		t.Fatalf("skip ปิด: ต้องหยุด broadcast ทันที (ไม่ replay) — ยัง broadcasting = bug")
	}
	if len(st.CurrentQueue) != 1 || st.CurrentQueue[0].QueueID != "N" {
		t.Fatalf("skip ปิด: ควร restore กลับเพลงปกติ N รอบเดียว, ได้ %+v", st.CurrentQueue)
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
