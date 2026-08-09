package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

const (
	privateAZoneID = "private-a"
	privateAX      = 200.0
	privateAY      = 200.0
	outsideAX      = 260.0
	outsideAY      = 200.0
)

func placeOutsidePrivateA(client *hub.Client) {
	client.LastX = outsideAX
	client.LastY = outsideAY
	client.LastZoneID = ""
	client.LastDir = "left"
	client.LastPresenceAt = time.Now().Add(-time.Second)
}

func tryEnterPrivateA(h *hub.Hub, client *hub.Client) {
	placeOutsidePrivateA(client)
	payload, _ := json.Marshal(presenceUpdatePayload{X: privateAX, Y: privateAY, Dir: "left"})
	HandlePresenceUpdate(h, client, payload)
}

func TestPrivateStrangerRejectedWhenOccupied(t *testing.T) {
	const roomID = "400001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, _, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	// Alice walks into empty private-a (first entrant).
	tryEnterPrivateA(h, alice)
	if alice.LastZoneID != privateAZoneID {
		t.Fatalf("alice zone = %q, want %q", alice.LastZoneID, privateAZoneID)
	}
	st, err := fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if err != nil {
		t.Fatalf("GetPrivateZoneState: %v", err)
	}
	if !containsID(st.Occupants, alice.ID) {
		t.Fatalf("alice should be occupant, got %+v", st)
	}

	// Bob (stranger) tries to enter occupied private → rejected.
	tryEnterPrivateA(h, bob)
	if bob.LastZoneID == privateAZoneID {
		t.Fatal("stranger must not enter occupied private zone")
	}
	if bob.LastX != outsideAX || bob.LastY != outsideAY {
		t.Fatalf("bob pos = (%v,%v), want outside (%v,%v)", bob.LastX, bob.LastY, outsideAX, outsideAY)
	}
	_ = waitCollectedEvent(t, inboxB, "presence_corrected", 2*time.Second, nil)

	st, _ = fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if containsID(st.Occupants, bob.ID) {
		t.Fatalf("bob must not be occupant, got %+v", st)
	}
}

func TestPrivateInviteeAccepted(t *testing.T) {
	const roomID = "400002"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, _, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	tryEnterPrivateA(h, alice)
	if alice.LastZoneID != privateAZoneID {
		t.Fatalf("alice zone = %q, want %q", alice.LastZoneID, privateAZoneID)
	}

	// Stranger rejected before invite.
	tryEnterPrivateA(h, bob)
	if bob.LastZoneID == privateAZoneID {
		t.Fatal("bob entered before invite")
	}
	_ = waitCollectedEvent(t, inboxB, "presence_corrected", 2*time.Second, nil)

	invitePayload, _ := json.Marshal(privateInvitePayload{
		ZoneID:           privateAZoneID,
		ToConnectionID:   bob.ID,
	})
	HandlePrivateInvite(h, alice, invitePayload)

	st, err := fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if err != nil {
		t.Fatalf("GetPrivateZoneState: %v", err)
	}
	if !containsID(st.Invites, bob.ID) {
		t.Fatalf("bob should be invited, got %+v", st)
	}

	tryEnterPrivateA(h, bob)
	if bob.LastZoneID != privateAZoneID {
		t.Fatalf("invitee zone = %q, want %q", bob.LastZoneID, privateAZoneID)
	}
	if bob.LastX != privateAX || bob.LastY != privateAY {
		t.Fatalf("invitee pos = (%v,%v), want (%v,%v)", bob.LastX, bob.LastY, privateAX, privateAY)
	}

	st, _ = fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if !containsID(st.Occupants, bob.ID) {
		t.Fatalf("invitee should become occupant, got %+v", st)
	}
}

func TestPrivateNonOccupantCannotInvite(t *testing.T) {
	const roomID = "400003"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, _, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, _, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()
	carol, inboxC, cleanupC := registerJoined(t, h, roomID, "carol")
	defer cleanupC()

	tryEnterPrivateA(h, alice)

	// Carol is outside — must not invite Bob.
	payload, _ := json.Marshal(privateInvitePayload{
		ZoneID:         privateAZoneID,
		ToConnectionID: bob.ID,
	})
	HandlePrivateInvite(h, carol, payload)

	errPayload := waitCollectedEvent(t, inboxC, "error", 2*time.Second, nil)
	if errPayload["code"] != "NOT_OCCUPANT" {
		t.Fatalf("error code = %v, want NOT_OCCUPANT", errPayload["code"])
	}

	st, err := fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if err != nil {
		t.Fatalf("GetPrivateZoneState: %v", err)
	}
	if containsID(st.Invites, bob.ID) {
		t.Fatalf("non-occupant invite must not add invite, got %+v", st)
	}
}

func TestPrivateLeaveClearsOccupantAndInvitesWhenEmpty(t *testing.T) {
	const roomID = "400004"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, _, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, _, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	tryEnterPrivateA(h, alice)
	invitePayload, _ := json.Marshal(privateInvitePayload{
		ZoneID:         privateAZoneID,
		ToConnectionID: bob.ID,
	})
	HandlePrivateInvite(h, alice, invitePayload)

	// Alice walks out to open floor.
	alice.LastPresenceAt = time.Now().Add(-time.Second)
	out, _ := json.Marshal(presenceUpdatePayload{X: outsideAX, Y: outsideAY, Dir: "right"})
	HandlePresenceUpdate(h, alice, out)

	if alice.LastZoneID == privateAZoneID {
		t.Fatal("alice should have left private zone")
	}
	st, err := fs.GetPrivateZoneState(context.Background(), roomID, privateAZoneID)
	if err != nil {
		t.Fatalf("GetPrivateZoneState: %v", err)
	}
	if len(st.Occupants) != 0 {
		t.Fatalf("occupants after leave = %+v, want empty", st.Occupants)
	}
	if len(st.Invites) != 0 {
		t.Fatalf("invites should clear when empty, got %+v", st.Invites)
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

var _ = model.PrivateZoneState{}
