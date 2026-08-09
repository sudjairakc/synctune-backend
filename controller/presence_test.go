package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/olahol/melody"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/office"
)

// dialTestSession สร้าง melody session จริงผ่าน httptest + gorilla websocket
func dialTestSession(t *testing.T) (*melody.Session, *websocket.Conn, func()) {
	t.Helper()
	m := melody.New()
	ready := make(chan *melody.Session, 1)
	m.HandleConnect(func(s *melody.Session) {
		ready <- s
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = m.HandleRequest(w, r)
	}))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial ws: %v", err)
	}

	var sess *melody.Session
	select {
	case sess = <-ready:
	case <-time.After(2 * time.Second):
		_ = conn.Close()
		srv.Close()
		t.Fatal("timeout waiting for melody connect")
	}

	cleanup := func() {
		_ = conn.Close()
		srv.Close()
	}
	return sess, conn, cleanup
}

func joinRoom(t *testing.T, h *hub.Hub, client *hub.Client, roomID, username string) {
	t.Helper()
	payload, _ := json.Marshal(joinPayload{Username: username, RoomID: roomID})
	HandleJoin(h, client, payload)
}

func TestJoinSetsSpawnPresence(t *testing.T) {
	const roomID = "100001"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sess, _, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	clientID, _ := sess.Get("client_id")
	client := h.GetClient(clientID.(string))
	if client == nil {
		t.Fatal("client not registered")
	}

	joinRoom(t, h, client, roomID, "alice")

	got, err := fs.GetAllPresence(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("presence count = %d, want 1", len(got))
	}
	p := got[0]
	if p.ConnectionID != client.ID {
		t.Fatalf("connection_id = %q, want %q", p.ConnectionID, client.ID)
	}
	if p.UserID != client.User.ID {
		t.Fatalf("user_id = %q, want %q", p.UserID, client.User.ID)
	}
	if p.X != office.SpawnX || p.Y != office.SpawnY {
		t.Fatalf("spawn pos = (%v,%v), want (%v,%v)", p.X, p.Y, office.SpawnX, office.SpawnY)
	}
	if client.LastX != office.SpawnX || client.LastY != office.SpawnY {
		t.Fatalf("client last pos not set to spawn")
	}
}

func TestPresenceUpdateMoves(t *testing.T) {
	const roomID = "100002"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sess, _, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	clientID, _ := sess.Get("client_id")
	client := h.GetClient(clientID.(string))

	joinRoom(t, h, client, roomID, "bob")

	// backdate so speed clamp allows a short walk
	client.LastPresenceAt = time.Now().Add(-time.Second)
	targetX := office.SpawnX + 100
	targetY := office.SpawnY
	payload, _ := json.Marshal(presenceUpdatePayload{X: targetX, Y: targetY, Dir: "right"})
	HandlePresenceUpdate(h, client, payload)

	got, err := fs.GetAllPresence(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("presence count = %d, want 1", len(got))
	}
	p := got[0]
	if p.X != targetX || p.Y != targetY {
		t.Fatalf("moved pos = (%v,%v), want (%v,%v)", p.X, p.Y, targetX, targetY)
	}
	if p.Dir != "right" {
		t.Fatalf("dir = %q, want right", p.Dir)
	}
	if client.LastX != targetX || client.LastY != targetY {
		t.Fatalf("client last not updated")
	}
}

// collectWSEvents reads conn until closed; returns a channel of decoded messages.
func collectWSEvents(conn *websocket.Conn) <-chan map[string]interface{} {
	ch := make(chan map[string]interface{}, 32)
	go func() {
		defer close(ch)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]interface{}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			ch <- msg
		}
	}()
	return ch
}

func waitCollectedEvent(t *testing.T, ch <-chan map[string]interface{}, event string, timeout time.Duration, match func(map[string]interface{}) bool) map[string]interface{} {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for event %q", event)
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("ws closed while waiting for event %q", event)
			}
			if msg["event"] != event {
				continue
			}
			payload, _ := msg["payload"].(map[string]interface{})
			if match != nil && !match(payload) {
				continue
			}
			return payload
		}
	}
}

func TestJoinBroadcastsSpawnPresenceToPeers(t *testing.T) {
	const roomID = "100004"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sessA, connA, cleanupA := dialTestSession(t)
	defer cleanupA()
	h.Register(sessA)
	clientAID, _ := sessA.Get("client_id")
	clientA := h.GetClient(clientAID.(string))
	inboxA := collectWSEvents(connA)

	joinRoom(t, h, clientA, roomID, "alice")
	_ = waitCollectedEvent(t, inboxA, "room_joined", 2*time.Second, nil)

	sessB, _, cleanupB := dialTestSession(t)
	defer cleanupB()
	h.Register(sessB)
	clientBID, _ := sessB.Get("client_id")
	clientB := h.GetClient(clientBID.(string))
	joinRoom(t, h, clientB, roomID, "bob")

	payload := waitCollectedEvent(t, inboxA, "presence_update", 2*time.Second, func(p map[string]interface{}) bool {
		return p["connection_id"] == clientB.ID
	})
	if payload["user_id"] != clientB.User.ID {
		t.Fatalf("presence_update user_id = %v, want %q", payload["user_id"], clientB.User.ID)
	}
	if payload["x"] != office.SpawnX || payload["y"] != office.SpawnY {
		t.Fatalf("presence_update pos = (%v,%v), want spawn", payload["x"], payload["y"])
	}
}

func TestPresenceUpdateTeleportCorrected(t *testing.T) {
	const roomID = "100003"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sess, conn, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	clientID, _ := sess.Get("client_id")
	client := h.GetClient(clientID.(string))
	inbox := collectWSEvents(conn)

	joinRoom(t, h, client, roomID, "carol")
	_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)

	// 100ms window @ 240 px/s → max ~24px; teleport far away must clamp
	client.LastPresenceAt = time.Now().Add(-100 * time.Millisecond)
	payload, _ := json.Marshal(presenceUpdatePayload{X: office.SpawnX + 500, Y: office.SpawnY, Dir: "right"})
	HandlePresenceUpdate(h, client, payload)

	got, err := fs.GetAllPresence(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("presence count = %d, want 1", len(got))
	}
	p := got[0]
	dx := p.X - office.SpawnX
	if dx > 30 || dx <= 0 {
		t.Fatalf("teleport should be speed-clamped near spawn, got x=%v (delta=%v)", p.X, dx)
	}
	if p.X == office.SpawnX+500 {
		t.Fatal("teleport accepted without correction")
	}

	corrected := waitCollectedEvent(t, inbox, "presence_corrected", 2*time.Second, nil)
	if _, ok := corrected["x"].(float64); !ok {
		t.Fatalf("presence_corrected missing x: %+v", corrected)
	}
	if _, ok := corrected["y"].(float64); !ok {
		t.Fatalf("presence_corrected missing y: %+v", corrected)
	}
	if corrected["dir"] != "right" {
		t.Fatalf("presence_corrected dir = %v, want right", corrected["dir"])
	}
	if _, ok := corrected["zone_id"].(string); !ok {
		t.Fatalf("presence_corrected missing zone_id: %+v", corrected)
	}
}

func TestPresenceLeaveOnDisconnect(t *testing.T) {
	const roomID = "100005"
	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sessA, _, cleanupA := dialTestSession(t)
	defer cleanupA()
	h.Register(sessA)
	idA, _ := sessA.Get("client_id")
	alice := h.GetClient(idA.(string))
	joinRoom(t, h, alice, roomID, "alice")

	_, inboxB, cleanupB := registerJoined(t, h, roomID, "bob")
	defer cleanupB()

	aliceID := alice.ID
	aliceUID := alice.User.ID
	h.Unregister(sessA)

	leave := waitCollectedEvent(t, inboxB, "presence_leave", 2*time.Second, func(p map[string]interface{}) bool {
		return p["connection_id"] == aliceID
	})
	if leave["user_id"] != aliceUID {
		t.Fatalf("presence_leave user_id = %v, want %q", leave["user_id"], aliceUID)
	}
	got, err := fs.GetAllPresence(context.Background(), roomID)
	if err != nil {
		t.Fatalf("GetAllPresence: %v", err)
	}
	for _, p := range got {
		if p.ConnectionID == aliceID {
			t.Fatal("alice presence should be deleted on disconnect")
		}
	}
}
