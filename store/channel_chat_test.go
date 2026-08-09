package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/synctune/backend/model"
)

func sampleChat(id, text string) model.ChatMessage {
	return model.ChatMessage{
		ID:        id,
		User:      model.User{ID: "u1", Username: "alice"},
		Text:      text,
		Timestamp: 1_700_000_000_000,
	}
}

func TestPushGetChannelHistory(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"
	channels := []string{
		"meeting:meeting-a",
		"dm:idA:idB",
		"bubble:bid",
	}

	for _, ch := range channels {
		msg := sampleChat("m-"+ch, "hello "+ch)
		if err := s.PushChannelMessage(ctx, roomID, ch, msg); err != nil {
			t.Fatalf("PushChannelMessage %s: %v", ch, err)
		}
		key := "synctune:room:" + roomID + ":chat:" + ch
		if !mr.Exists(key) {
			t.Fatalf("expected key %q after PushChannelMessage", key)
		}

		got, err := s.GetChannelHistory(ctx, roomID, ch, 50)
		if err != nil {
			t.Fatalf("GetChannelHistory %s: %v", ch, err)
		}
		if len(got) != 1 {
			t.Fatalf("GetChannelHistory %s len = %d, want 1", ch, len(got))
		}
		if got[0].ID != msg.ID || got[0].Text != msg.Text || got[0].User != msg.User || got[0].Timestamp != msg.Timestamp {
			t.Errorf("GetChannelHistory %s = %+v, want %+v", ch, got[0], msg)
		}
	}
}

func TestChannelHistoryNewestFirstAndLimit(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"
	const channel = "meeting:zone-1"

	for i := 1; i <= 5; i++ {
		msg := sampleChat(fmt.Sprintf("m%d", i), fmt.Sprintf("msg-%d", i))
		if err := s.PushChannelMessage(ctx, roomID, channel, msg); err != nil {
			t.Fatalf("PushChannelMessage %d: %v", i, err)
		}
	}

	got, err := s.GetChannelHistory(ctx, roomID, channel, 3)
	if err != nil {
		t.Fatalf("GetChannelHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "m5" || got[1].ID != "m4" || got[2].ID != "m3" {
		t.Errorf("order = %s,%s,%s want m5,m4,m3", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestChannelHistoryTrimmedToMaxChat(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"
	const channel = "bubble:b1"

	for i := 0; i < maxChat+10; i++ {
		msg := sampleChat(fmt.Sprintf("m%d", i), "x")
		if err := s.PushChannelMessage(ctx, roomID, channel, msg); err != nil {
			t.Fatalf("PushChannelMessage %d: %v", i, err)
		}
	}

	got, err := s.GetChannelHistory(ctx, roomID, channel, maxChat+50)
	if err != nil {
		t.Fatalf("GetChannelHistory: %v", err)
	}
	if len(got) != maxChat {
		t.Fatalf("len = %d, want maxChat=%d", len(got), maxChat)
	}
	if got[0].ID != fmt.Sprintf("m%d", maxChat+9) {
		t.Errorf("newest ID = %q, want m%d", got[0].ID, maxChat+9)
	}
}

func TestChannelHistoriesAreIsolated(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	_ = s.PushChannelMessage(ctx, roomID, "meeting:a", sampleChat("meet", "m"))
	_ = s.PushChannelMessage(ctx, roomID, "dm:u1:u2", sampleChat("dm", "d"))
	_ = s.PushChatMessage(ctx, roomID, sampleChat("room", "r"))

	meet, _ := s.GetChannelHistory(ctx, roomID, "meeting:a", 10)
	dm, _ := s.GetChannelHistory(ctx, roomID, "dm:u1:u2", 10)
	room, _ := s.GetChatHistory(ctx, roomID)

	if len(meet) != 1 || meet[0].ID != "meet" {
		t.Errorf("meeting history = %+v", meet)
	}
	if len(dm) != 1 || dm[0].ID != "dm" {
		t.Errorf("dm history = %+v", dm)
	}
	if len(room) != 1 || room[0].ID != "room" {
		t.Errorf("room chat history = %+v", room)
	}
}

func TestDeleteRoomClearsChannelChatKeys(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	const roomID = "room-1"

	channels := []string{"meeting:meeting-a", "dm:idA:idB", "bubble:bid"}
	for _, ch := range channels {
		if err := s.PushChannelMessage(ctx, roomID, ch, sampleChat(ch, "x")); err != nil {
			t.Fatalf("PushChannelMessage %s: %v", ch, err)
		}
	}
	if err := s.PushChatMessage(ctx, roomID, sampleChat("room", "r")); err != nil {
		t.Fatalf("PushChatMessage: %v", err)
	}

	otherRoomKey := "synctune:room:other:chat:meeting:a"
	_ = s.PushChannelMessage(ctx, "other", "meeting:a", sampleChat("keep", "k"))

	if err := s.DeleteRoom(ctx, roomID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}

	for _, ch := range channels {
		key := "synctune:room:" + roomID + ":chat:" + ch
		if mr.Exists(key) {
			t.Errorf("channel key %q still exists after DeleteRoom", key)
		}
	}
	if mr.Exists(roomChatKey(roomID)) {
		t.Errorf("room chat key still exists after DeleteRoom")
	}
	if !mr.Exists(otherRoomKey) {
		t.Errorf("other room channel key %q should remain", otherRoomKey)
	}
}

func TestGetChannelHistoryEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.GetChannelHistory(context.Background(), "room-1", "meeting:x", 10)
	if err != nil {
		t.Fatalf("GetChannelHistory: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
