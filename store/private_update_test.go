package store

import (
	"context"
	"testing"

	"github.com/synctune/backend/model"
)

func TestUpdatePrivateZoneStateConcurrentInvites(t *testing.T) {
	s, mr := newTestStore(t)
	defer mr.Close()
	ctx := context.Background()
	const roomID = "priv-race-1"
	const zoneID = "private-a"

	if err := s.SetPrivateZoneState(ctx, roomID, zoneID, &model.PrivateZoneState{
		Occupants: []string{"alice"},
		Invites:   []string{},
	}); err != nil {
		t.Fatal(err)
	}

	err := s.UpdatePrivateZoneState(ctx, roomID, zoneID, func(st *model.PrivateZoneState) (bool, error) {
		st.Invites = append(st.Invites, "bob")
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.UpdatePrivateZoneState(ctx, roomID, zoneID, func(st *model.PrivateZoneState) (bool, error) {
		st.Invites = append(st.Invites, "carol")
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Invites) != 2 {
		t.Fatalf("invites=%v want bob+carol", got.Invites)
	}
}

func TestUpdatePrivateZoneStateDeleteWhenEmpty(t *testing.T) {
	s, mr := newTestStore(t)
	defer mr.Close()
	ctx := context.Background()
	const roomID = "priv-race-2"
	const zoneID = "private-a"

	_ = s.SetPrivateZoneState(ctx, roomID, zoneID, &model.PrivateZoneState{
		Occupants: []string{"alice"},
		Invites:   []string{"bob"},
	})
	err := s.UpdatePrivateZoneState(ctx, roomID, zoneID, func(st *model.PrivateZoneState) (bool, error) {
		st.Occupants = nil
		st.Invites = nil
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPrivateZoneState(ctx, roomID, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Occupants) != 0 || len(got.Invites) != 0 {
		t.Fatalf("expected empty after delete, got %+v", got)
	}
}
