package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

func TestBubbleLifecycle_ABC_ADisconnect_BCRemain_LastLeaveGone(t *testing.T) {
	const roomID = "500001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	aliceSess, aliceConn, cleanupA := dialTestSession(t)
	defer cleanupA()
	h.Register(aliceSess)
	aliceID, _ := aliceSess.Get("client_id")
	alice := h.GetClient(aliceID.(string))
	aliceInbox := collectWSEvents(aliceConn)
	joinRoom(t, h, alice, roomID, "alice")
	_ = waitCollectedEvent(t, aliceInbox, "room_joined", 2*time.Second, nil)

	bob, bobInbox, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()
	carol, carolInbox, cleanupC := registerJoined(t, h, roomID, "carol")
	defer cleanupC()

	// A invites B → creates bubble with A as member + pending invite for B
	inviteB, _ := json.Marshal(bubbleInvitePayload{ToConnectionID: bob.ID})
	HandleBubbleInvite(h, alice, inviteB)

	inviteEvt := waitCollectedEvent(t, bobInbox, "bubble_invite", 2*time.Second, nil)
	bubbleID, _ := inviteEvt["bubble_id"].(string)
	if bubbleID == "" {
		t.Fatal("bubble_invite missing bubble_id")
	}

	b, err := fs.GetBubble(context.Background(), roomID, bubbleID)
	if err != nil || b == nil {
		t.Fatalf("GetBubble after invite: %v %#v", err, b)
	}
	if len(b.Members) != 1 || b.Members[0] != alice.ID {
		t.Fatalf("members after invite = %+v, want [alice]", b.Members)
	}
	if !containsID(b.Invites, bob.ID) {
		t.Fatalf("invites after invite = %+v, want bob", b.Invites)
	}
	if alice.BubbleID != bubbleID {
		t.Fatalf("alice.BubbleID = %q, want %q", alice.BubbleID, bubbleID)
	}

	// B accepts
	acceptB, _ := json.Marshal(bubbleAcceptPayload{BubbleID: bubbleID})
	HandleBubbleAccept(h, bob, acceptB)
	_ = waitCollectedEvent(t, bobInbox, "bubble_updated", 2*time.Second, func(p map[string]interface{}) bool {
		if p["bubble_id"] != bubbleID {
			return false
		}
		members, _ := p["members"].([]interface{})
		return len(members) == 2
	})
	if bob.BubbleID != bubbleID {
		t.Fatalf("bob.BubbleID = %q, want %q", bob.BubbleID, bubbleID)
	}

	// A invites C into same bubble
	inviteC, _ := json.Marshal(bubbleInvitePayload{ToConnectionID: carol.ID})
	HandleBubbleInvite(h, alice, inviteC)
	_ = waitCollectedEvent(t, carolInbox, "bubble_invite", 2*time.Second, nil)

	acceptC, _ := json.Marshal(bubbleAcceptPayload{BubbleID: bubbleID})
	HandleBubbleAccept(h, carol, acceptC)
	_ = waitCollectedEvent(t, carolInbox, "bubble_updated", 2*time.Second, func(p map[string]interface{}) bool {
		if p["bubble_id"] != bubbleID {
			return false
		}
		members, _ := p["members"].([]interface{})
		return len(members) == 3
	})

	b, _ = fs.GetBubble(context.Background(), roomID, bubbleID)
	if b == nil || len(b.Members) != 3 {
		t.Fatalf("want 3 members before disconnect, got %#v", b)
	}
	for _, id := range []string{alice.ID, bob.ID, carol.ID} {
		if !containsID(b.Members, id) {
			t.Fatalf("missing member %s in %+v", id, b.Members)
		}
	}

	// A disconnects → B,C remain (ownerless)
	h.Unregister(aliceSess)

	_ = waitCollectedEvent(t, bobInbox, "bubble_updated", 2*time.Second, func(p map[string]interface{}) bool {
		if p["bubble_id"] != bubbleID {
			return false
		}
		members, _ := p["members"].([]interface{})
		return len(members) == 2
	})

	b, err = fs.GetBubble(context.Background(), roomID, bubbleID)
	if err != nil || b == nil {
		t.Fatalf("bubble should continue after A disconnect: %v %#v", err, b)
	}
	if containsID(b.Members, alice.ID) {
		t.Fatalf("alice should be removed, got %+v", b.Members)
	}
	if !containsID(b.Members, bob.ID) || !containsID(b.Members, carol.ID) {
		t.Fatalf("B+C should remain, got %+v", b.Members)
	}
	if len(b.Members) != 2 {
		t.Fatalf("members = %+v, want exactly B+C", b.Members)
	}
	if bob.BubbleID != bubbleID || carol.BubbleID != bubbleID {
		t.Fatalf("B/C BubbleID cleared unexpectedly: bob=%q carol=%q", bob.BubbleID, carol.BubbleID)
	}

	// B leaves → C alone
	HandleBubbleLeave(h, bob, json.RawMessage(`{}`))
	_ = waitCollectedEvent(t, carolInbox, "bubble_updated", 2*time.Second, func(p map[string]interface{}) bool {
		if p["bubble_id"] != bubbleID {
			return false
		}
		members, _ := p["members"].([]interface{})
		return len(members) == 1
	})
	if bob.BubbleID != "" {
		t.Fatalf("bob.BubbleID after leave = %q, want empty", bob.BubbleID)
	}

	// Last leave (C) → bubble gone
	HandleBubbleLeave(h, carol, json.RawMessage(`{}`))
	_ = waitCollectedEvent(t, carolInbox, "bubble_updated", 2*time.Second, func(p map[string]interface{}) bool {
		if p["bubble_id"] != bubbleID {
			return false
		}
		members, _ := p["members"].([]interface{})
		return len(members) == 0
	})

	b, err = fs.GetBubble(context.Background(), roomID, bubbleID)
	if err != nil {
		t.Fatalf("GetBubble after last leave: %v", err)
	}
	if b != nil {
		t.Fatalf("bubble should be deleted, got %#v", b)
	}
	if carol.BubbleID != "" {
		t.Fatalf("carol.BubbleID after last leave = %q, want empty", carol.BubbleID)
	}
}

func TestBubbleHasNoOwnerField(t *testing.T) {
	// Compile-time / JSON shape: Bubble must not serialize an owner field.
	b := model.Bubble{ID: "x", Members: []string{"a"}, Invites: []string{"b"}}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["owner"]; ok {
		t.Fatal("Bubble JSON must not include owner")
	}
	if _, ok := m["Owner"]; ok {
		t.Fatal("Bubble JSON must not include Owner")
	}
}
