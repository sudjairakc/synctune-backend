package controller

import (
	"context"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

func TestDmChannelIncludesUser(t *testing.T) {
	cases := []struct {
		ch, uid string
		want    bool
	}{
		{"dm:aaa:bbb", "aaa", true},
		{"dm:aaa:bbb", "bbb", true},
		{"dm:aaa:bbb", "ccc", false},
		{"meeting:meeting-a", "aaa", false},
		{"dm:aaa", "aaa", false},
		{"", "aaa", false},
	}
	for _, tc := range cases {
		if got := dmChannelIncludesUser(tc.ch, tc.uid); got != tc.want {
			t.Fatalf("dmChannelIncludesUser(%q,%q)=%v want %v", tc.ch, tc.uid, got, tc.want)
		}
	}
}

func TestLoadJoinChannelHistories_MeetingBubbleDM(t *testing.T) {
	const roomID = "210001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	client, _, cleanup := registerJoined(t, h, roomID, "alice")
	defer cleanup()
	bob, _, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	ids := []string{client.User.ID, bob.User.ID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	dmCh := "dm:" + ids[0] + ":" + ids[1]
	meetCh := "meeting:meeting-a"
	bubbleID := "bub-hydrate-1"
	bubbleCh := "bubble:" + bubbleID

	now := time.Now().UnixMilli()
	_ = fs.PushChannelMessage(context.Background(), roomID, meetCh, model.ChatMessage{
		ID: "m1", Text: "standup", Timestamp: now, User: model.User{ID: "u", Username: "mod"},
	})
	_ = fs.PushChannelMessage(context.Background(), roomID, bubbleCh, model.ChatMessage{
		ID: "b1", Text: "huddle", Timestamp: now, User: model.User{ID: "u", Username: "mod"},
	})
	_ = fs.PushChannelMessage(context.Background(), roomID, dmCh, model.ChatMessage{
		ID: "d1", Text: "psst", Timestamp: now, User: bob.User,
	})
	_ = fs.PushChannelMessage(context.Background(), roomID, "dm:zzz:yyy", model.ChatMessage{
		ID: "d2", Text: "nope", Timestamp: now, User: model.User{ID: "zzz", Username: "z"},
	})

	client.BubbleID = bubbleID
	ax, ay := office.MeetingCenter(office.DefaultMap(), "meeting-a")
	spawned := model.Presence{
		ConnectionID: client.ID,
		UserID:       client.User.ID,
		X:            ax,
		Y:            ay,
		BubbleID:     bubbleID,
	}

	got := loadJoinChannelHistories(h, client, spawned)
	if len(got[meetCh]) != 1 || got[meetCh][0].Text != "standup" {
		t.Fatalf("meeting history = %#v", got[meetCh])
	}
	if len(got[bubbleCh]) != 1 || got[bubbleCh][0].Text != "huddle" {
		t.Fatalf("bubble history = %#v", got[bubbleCh])
	}
	if len(got[dmCh]) != 1 || got[dmCh][0].Text != "psst" {
		t.Fatalf("dm history = %#v", got[dmCh])
	}
	if _, ok := got["dm:zzz:yyy"]; ok {
		t.Fatal("unrelated DM must not hydrate")
	}
}

func TestJoinRoom_HydratesDMInChannelHistories(t *testing.T) {
	const roomID = "210002"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, _, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()

	// Register bob before join — User.ID == connection_id after join.
	sessB, connB, cleanupB := dialTestSession(t)
	defer cleanupB()
	h.Register(sessB)
	idB, _ := sessB.Get("client_id")
	bob := h.GetClient(idB.(string))

	ids := []string{alice.User.ID, bob.ID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	dmCh := "dm:" + ids[0] + ":" + ids[1]
	_ = fs.PushChannelMessage(context.Background(), roomID, dmCh, model.ChatMessage{
		ID: "d1", Text: "welcome back", Timestamp: time.Now().UnixMilli(), User: alice.User,
	})
	_ = fs.PushChannelMessage(context.Background(), roomID, "meeting:meeting-a", model.ChatMessage{
		ID: "m1", Text: "not at spawn", Timestamp: time.Now().UnixMilli(),
		User: model.User{ID: "mod", Username: "mod"},
	})

	inbox := collectWSEvents(connB)
	joinRoom(t, h, bob, roomID, "bob")
	joined := waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)

	chRaw, _ := joined["channel_histories"].(map[string]interface{})
	if chRaw == nil {
		t.Fatal("room_joined missing channel_histories")
	}
	dmMsgs, ok := chRaw[dmCh].([]interface{})
	if !ok || len(dmMsgs) != 1 {
		t.Fatalf("channel_histories[%s] = %#v", dmCh, chRaw[dmCh])
	}
	msg0, _ := dmMsgs[0].(map[string]interface{})
	if msg0["text"] != "welcome back" {
		t.Fatalf("dm text = %v", msg0["text"])
	}
	if _, ok := chRaw["meeting:meeting-a"]; ok {
		t.Fatal("open spawn must not hydrate meeting history")
	}

	// Sanity: spawn still open floor.
	if _, zt := office.DefaultMap().ZoneAt(office.SpawnX, office.SpawnY); zt != office.ZoneOpen {
		t.Fatalf("spawn zone = %s, want open", zt)
	}
}
