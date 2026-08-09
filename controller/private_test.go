package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/office"
)

func TestDefaultMapHasNoPrivateZones(t *testing.T) {
	m := office.DefaultMap()
	if m.IsPrivateZone("private-a") || m.IsPrivateZone("private-b") {
		t.Fatal("map v2 must not report private-a/private-b")
	}
}

func TestWalkingIntoDeskYieldsPresenceCorrected(t *testing.T) {
	const roomID = "400010"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	client, inbox, cleanup := registerJoined(t, h, roomID, "alice")
	defer cleanup()

	m := office.DefaultMap()
	outX, outY, deskX, deskY := office.DeskProbeOutside(m)
	if m.IsWalkable(deskX, deskY) {
		t.Fatal("desk probe target must be non-walkable")
	}

	client.LastX, client.LastY = outX, outY
	client.LastZoneID = ""
	client.LastPresenceAt = time.Now().Add(-time.Second)
	payload, _ := json.Marshal(presenceUpdatePayload{X: deskX, Y: deskY, Dir: "down"})
	HandlePresenceUpdate(h, client, payload)

	if client.LastX != outX || client.LastY != outY {
		t.Fatalf("desk collision should revert, got (%v,%v) want (%v,%v)", client.LastX, client.LastY, outX, outY)
	}
	_ = waitCollectedEvent(t, inbox, "presence_corrected", 2*time.Second, nil)
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestPrivateInviteRejectsNonPrivateZone(t *testing.T) {
	const roomID = "400011"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, _, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	for _, zoneID := range []string{"private-a", "meeting-a"} {
		payload, _ := json.Marshal(privateInvitePayload{
			ZoneID:         zoneID,
			ToConnectionID: bob.ID,
		})
		HandlePrivateInvite(h, alice, payload)
		errPayload := waitCollectedEvent(t, inboxA, "error", 2*time.Second, nil)
		if errPayload["code"] != "INVALID_ZONE" {
			t.Fatalf("zone %s: error code = %v, want INVALID_ZONE", zoneID, errPayload["code"])
		}
	}
}
