package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/synctune/backend/model"
)

func newTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(mr.Close)

	s, err := NewRedisStore(mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return s, mr
}

func samplePresence(connID string) model.Presence {
	return model.Presence{
		ConnectionID: connID,
		UserID:       "u-" + connID,
		Username:     "user-" + connID,
		ProfileImg:   "https://img/" + connID,
		X:            100.5,
		Y:            200.25,
		Dir:          "down",
		ZoneID:       "lobby",
	}
}

func TestSetGetAllPresence(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	p1 := samplePresence("c1")
	p2 := samplePresence("c2")
	p2.X, p2.Y, p2.Dir, p2.ZoneID = 10, 20, "left", "meeting"

	if err := s.SetPresence(ctx, roomID, p1); err != nil {
		t.Fatalf("SetPresence p1: %v", err)
	}
	if err := s.SetPresence(ctx, roomID, p2); err != nil {
		t.Fatalf("SetPresence p2: %v", err)
	}

	got, err := s.GetAllPresence(ctx, roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetAllPresence len = %d, want 2", len(got))
	}

	byConn := map[string]model.Presence{}
	for _, p := range got {
		byConn[p.ConnectionID] = p
	}
	if byConn["c1"] != p1 {
		t.Errorf("presence c1 = %+v, want %+v", byConn["c1"], p1)
	}
	if byConn["c2"] != p2 {
		t.Errorf("presence c2 = %+v, want %+v", byConn["c2"], p2)
	}
}

func TestSetPresenceOverwrite(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	p := samplePresence("c1")
	if err := s.SetPresence(ctx, roomID, p); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	p.X, p.Y, p.Dir = 50, 60, "up"
	if err := s.SetPresence(ctx, roomID, p); err != nil {
		t.Fatalf("SetPresence overwrite: %v", err)
	}

	got, err := s.GetAllPresence(ctx, roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != p {
		t.Errorf("got %+v, want %+v", got[0], p)
	}
}

func TestGetAllPresenceEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.GetAllPresence(context.Background(), "empty-room")
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestDeletePresence(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	_ = s.SetPresence(ctx, roomID, samplePresence("c1"))
	_ = s.SetPresence(ctx, roomID, samplePresence("c2"))

	if err := s.DeletePresence(ctx, roomID, "c1"); err != nil {
		t.Fatalf("DeletePresence: %v", err)
	}

	got, err := s.GetAllPresence(ctx, roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ConnectionID != "c2" {
		t.Errorf("remaining = %q, want c2", got[0].ConnectionID)
	}
}

func TestDeleteRoomClearsPresence(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	if err := s.SetPresence(ctx, roomID, samplePresence("c1")); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	key := "synctune:room:" + roomID + ":presence"
	if !mr.Exists(key) {
		t.Fatalf("expected presence key %q to exist", key)
	}

	if err := s.DeleteRoom(ctx, roomID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	if mr.Exists(key) {
		t.Errorf("presence key %q still exists after DeleteRoom", key)
	}

	got, err := s.GetAllPresence(ctx, roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len after DeleteRoom = %d, want 0", len(got))
	}
}
