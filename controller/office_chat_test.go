package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
)

const (
	meetingZoneID = "meeting-a"
	meetingX      = 1088.0 // inside meeting-a rect
	meetingY      = 256.0
)

func placeInMeeting(c *hub.Client) {
	c.LastX = meetingX
	c.LastY = meetingY
	c.LastZoneID = meetingZoneID
}

func assertNoEvent(t *testing.T, ch <-chan map[string]interface{}, event string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case <-deadline:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg["event"] == event {
				t.Fatalf("unexpected event %q: %+v", event, msg["payload"])
			}
		}
	}
}

func registerJoined(t *testing.T, h *hub.Hub, roomID, username string) (*hub.Client, <-chan map[string]interface{}, func()) {
	t.Helper()
	sess, conn, cleanup := dialTestSession(t)
	h.Register(sess)
	clientID, _ := sess.Get("client_id")
	client := h.GetClient(clientID.(string))
	if client == nil {
		cleanup()
		t.Fatal("client not registered")
	}
	inbox := collectWSEvents(conn)
	joinRoom(t, h, client, roomID, username)
	_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)
	return client, inbox, cleanup
}

func TestMeetingSendRejectOutsideZone(t *testing.T) {
	const roomID = "200001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	client, inbox, cleanup := registerJoined(t, h, roomID, "alice")
	defer cleanup()

	// spawn is open floor — meeting_send must be rejected
	payload, _ := json.Marshal(meetingSendPayload{Text: "hello meeting"})
	HandleMeetingSend(h, client, payload)

	errPayload := waitCollectedEvent(t, inbox, "error", 2*time.Second, nil)
	if errPayload["code"] != "NOT_IN_MEETING" {
		t.Fatalf("error code = %v, want NOT_IN_MEETING", errPayload["code"])
	}
	got, _ := fs.GetChannelHistory(context.Background(), roomID, "meeting:"+meetingZoneID, 10)
	if len(got) != 0 {
		t.Fatalf("should not persist meeting message outside zone, got %d", len(got))
	}
}

func TestMeetingSendBroadcastsOnlyInZone(t *testing.T) {
	const roomID = "200002"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()
	_, inboxC, cleanupC := registerJoined(t, h, roomID, "carol")
	defer cleanupC()

	placeInMeeting(alice)
	placeInMeeting(bob)
	// carol stays at spawn (open)

	payload, _ := json.Marshal(meetingSendPayload{Text: "stand-up notes"})
	HandleMeetingSend(h, alice, payload)

	msgA := waitCollectedEvent(t, inboxA, "meeting_message", 2*time.Second, nil)
	msgB := waitCollectedEvent(t, inboxB, "meeting_message", 2*time.Second, nil)
	assertNoEvent(t, inboxC, "meeting_message", 200*time.Millisecond)

	if msgA["channel"] != "meeting:"+meetingZoneID {
		t.Fatalf("alice channel = %v, want meeting:%s", msgA["channel"], meetingZoneID)
	}
	if msgB["channel"] != "meeting:"+meetingZoneID {
		t.Fatalf("bob channel = %v, want meeting:%s", msgB["channel"], meetingZoneID)
	}
	text, _ := msgA["text"].(string)
	if text != "stand-up notes" {
		t.Fatalf("text = %q, want stand-up notes", text)
	}

	hist, _ := fs.GetChannelHistory(context.Background(), roomID, "meeting:"+meetingZoneID, 10)
	if len(hist) != 1 {
		t.Fatalf("channel history len = %d, want 1", len(hist))
	}
}

func TestDMSendIsolation(t *testing.T) {
	const roomID = "200003"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inboxA, cleanupA := registerJoined(t, h, roomID, "alice")
	defer cleanupA()
	bob, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()
	_, inboxC, cleanupC := registerJoined(t, h, roomID, "carol")
	defer cleanupC()

	payload, _ := json.Marshal(dmSendPayload{ToConnectionID: bob.ID, Text: "secret ping"})
	HandleDMSend(h, alice, payload)

	msgA := waitCollectedEvent(t, inboxA, "dm_message", 2*time.Second, nil)
	msgB := waitCollectedEvent(t, inboxB, "dm_message", 2*time.Second, nil)
	assertNoEvent(t, inboxC, "dm_message", 200*time.Millisecond)

	ids := []string{alice.User.ID, bob.User.ID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	wantChannel := "dm:" + ids[0] + ":" + ids[1]
	if msgA["channel"] != wantChannel {
		t.Fatalf("alice channel = %v, want %s", msgA["channel"], wantChannel)
	}
	if msgB["channel"] != wantChannel {
		t.Fatalf("bob channel = %v, want %s", msgB["channel"], wantChannel)
	}
	if msgA["text"] != "secret ping" || msgB["text"] != "secret ping" {
		t.Fatalf("unexpected text: a=%v b=%v", msgA["text"], msgB["text"])
	}

	hist, _ := fs.GetChannelHistory(context.Background(), roomID, wantChannel, 10)
	if len(hist) != 1 {
		t.Fatalf("dm history len = %d, want 1", len(hist))
	}
}

func TestDMSendRejectMissingTarget(t *testing.T) {
	const roomID = "200004"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	alice, inbox, cleanup := registerJoined(t, h, roomID, "alice")
	defer cleanup()

	payload, _ := json.Marshal(dmSendPayload{ToConnectionID: "missing-conn", Text: "hi"})
	HandleDMSend(h, alice, payload)

	errPayload := waitCollectedEvent(t, inbox, "error", 2*time.Second, nil)
	if errPayload["code"] != "TARGET_NOT_FOUND" {
		t.Fatalf("error code = %v, want TARGET_NOT_FOUND", errPayload["code"])
	}
}
