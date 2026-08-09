package store

import (
	"context"
	"testing"

	"github.com/synctune/backend/model"
)

func TestPrivateZoneStateRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-p1"
	const zoneID = "private-a"

	got, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if got == nil || len(got.Occupants) != 0 || len(got.Invites) != 0 {
		t.Fatalf("empty state = %+v, want empty occupants/invites", got)
	}

	st := &model.PrivateZoneState{
		Occupants: []string{"c1"},
		Invites:   []string{"c2", "c3"},
	}
	if err := s.SetPrivateZoneState(ctx, roomID, zoneID, st); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err = s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Occupants) != 1 || got.Occupants[0] != "c1" {
		t.Fatalf("occupants = %+v", got.Occupants)
	}
	if len(got.Invites) != 2 {
		t.Fatalf("invites = %+v", got.Invites)
	}
}

func TestDeleteRoomClearsPrivateKeys(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-p2"

	_ = s.SetPrivateZoneState(ctx, roomID, "private-a", &model.PrivateZoneState{
		Occupants: []string{"c1"},
	})
	_ = s.SetPrivateZoneState(ctx, roomID, "private-b", &model.PrivateZoneState{
		Invites: []string{"c2"},
	})

	keyA := "synctune:room:" + roomID + ":private:private-a"
	keyB := "synctune:room:" + roomID + ":private:private-b"
	if !mr.Exists(keyA) || !mr.Exists(keyB) {
		t.Fatal("expected private keys to exist")
	}

	if err := s.DeleteRoom(ctx, roomID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	if mr.Exists(keyA) {
		t.Errorf("private key %q still exists after DeleteRoom", keyA)
	}
	if mr.Exists(keyB) {
		t.Errorf("private key %q still exists after DeleteRoom", keyB)
	}
}
