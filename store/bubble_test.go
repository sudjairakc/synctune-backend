package store

import (
	"context"
	"testing"

	"github.com/synctune/backend/model"
)

func TestBubbleRoundTripAndDeleteClearsChat(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-b1"
	const bubbleID = "bubble-1"

	got, err := s.GetBubble(ctx, roomID, bubbleID)
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil bubble, got %#v", got)
	}

	b := &model.Bubble{
		ID:      bubbleID,
		Members: []string{"a", "b"},
		Invites: []string{"c"},
	}
	if err := s.SetBubble(ctx, roomID, b); err != nil {
		t.Fatalf("SetBubble: %v", err)
	}

	got, err = s.GetBubble(ctx, roomID, bubbleID)
	if err != nil || got == nil {
		t.Fatalf("GetBubble: %v %#v", err, got)
	}
	if len(got.Members) != 2 || got.Members[0] != "a" {
		t.Fatalf("members = %+v", got.Members)
	}

	chatKey := "synctune:room:" + roomID + ":chat:bubble:" + bubbleID
	_ = s.PushChannelMessage(ctx, roomID, "bubble:"+bubbleID, model.ChatMessage{ID: "m1", Text: "hi"})
	if !mr.Exists(chatKey) {
		t.Fatal("expected bubble chat key")
	}

	if err := s.DeleteBubble(ctx, roomID, bubbleID); err != nil {
		t.Fatalf("DeleteBubble: %v", err)
	}
	got, _ = s.GetBubble(ctx, roomID, bubbleID)
	if got != nil {
		t.Fatalf("bubble should be gone, got %#v", got)
	}
	if mr.Exists(chatKey) {
		t.Errorf("chat key %q should be deleted with bubble", chatKey)
	}
}

func TestRemoveConnectionFromBubblesKeepsOthers(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-b2"
	const bubbleID = "bid"

	_ = s.SetBubble(ctx, roomID, &model.Bubble{
		ID:      bubbleID,
		Members: []string{"a", "b", "c"},
	})

	updated, err := s.RemoveConnectionFromBubbles(ctx, roomID, "a")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(updated) != 1 || len(updated[0].Members) != 2 {
		t.Fatalf("updated = %#v", updated)
	}

	got, _ := s.GetBubble(ctx, roomID, bubbleID)
	if got == nil || len(got.Members) != 2 {
		t.Fatalf("bubble after remove A = %#v", got)
	}
	for _, id := range got.Members {
		if id == "a" {
			t.Fatal("a should be removed")
		}
	}

	updated, err = s.RemoveConnectionFromBubbles(ctx, roomID, "b")
	if err != nil {
		t.Fatalf("Remove b: %v", err)
	}
	updated, err = s.RemoveConnectionFromBubbles(ctx, roomID, "c")
	if err != nil {
		t.Fatalf("Remove c: %v", err)
	}
	if len(updated) != 1 || len(updated[0].Members) != 0 {
		t.Fatalf("last remove updated = %#v", updated)
	}
	got, _ = s.GetBubble(ctx, roomID, bubbleID)
	if got != nil {
		t.Fatalf("bubble should be deleted when empty, got %#v", got)
	}
}

func TestDeleteRoomClearsBubblesKey(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-b3"

	_ = s.SetBubble(ctx, roomID, &model.Bubble{ID: "x", Members: []string{"a"}})
	key := "synctune:room:" + roomID + ":bubbles"
	if !mr.Exists(key) {
		t.Fatal("expected bubbles key")
	}
	if err := s.DeleteRoom(ctx, roomID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	if mr.Exists(key) {
		t.Errorf("bubbles key %q still exists after DeleteRoom", key)
	}
}
