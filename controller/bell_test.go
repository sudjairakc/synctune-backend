package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
)

func TestBellRingDeliversOnlyToTarget(t *testing.T) {
	const roomID = "300001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()
	_, inboxC, cleanupC := registerJoined(t, h, roomID, "carol")
	defer cleanupC()

	payload, _ := json.Marshal(bellRingPayload{TargetConnectionID: bob.ID})
	HandleBellRing(h, alice, payload)

	got := waitCollectedEvent(t, inboxB, "bell_ring", 2*time.Second, nil)
	if got["from_connection_id"] != alice.ID {
		t.Fatalf("from_connection_id = %v, want %q", got["from_connection_id"], alice.ID)
	}
	if got["from_user_id"] != alice.User.ID {
		t.Fatalf("from_user_id = %v, want %q", got["from_user_id"], alice.User.ID)
	}
	assertNoEvent(t, inboxA, "bell_ring", 200*time.Millisecond)
	assertNoEvent(t, inboxC, "bell_ring", 200*time.Millisecond)
}

func TestBellRingRateLimited(t *testing.T) {
	const roomID = "300002"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	payload, _ := json.Marshal(bellRingPayload{TargetConnectionID: bob.ID})
	HandleBellRing(h, alice, payload)
	_ = waitCollectedEvent(t, inboxB, "bell_ring", 2*time.Second, nil)

	HandleBellRing(h, alice, payload)
	errPayload := waitCollectedEvent(t, inboxA, "error", 2*time.Second, nil)
	if errPayload["code"] != "RATE_LIMITED" {
		t.Fatalf("error code = %v, want RATE_LIMITED", errPayload["code"])
	}
	assertNoEvent(t, inboxB, "bell_ring", 200*time.Millisecond)
}

func TestBellRingMissingTarget(t *testing.T) {
	const roomID = "300003"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()

	payload, _ := json.Marshal(bellRingPayload{TargetConnectionID: "missing-conn"})
	HandleBellRing(h, alice, payload)

	errPayload := waitCollectedEvent(t, inboxA, "error", 2*time.Second, nil)
	if errPayload["code"] != "TARGET_NOT_FOUND" {
		t.Fatalf("error code = %v, want TARGET_NOT_FOUND", errPayload["code"])
	}
}
