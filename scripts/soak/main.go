// Automated office-layer soak (WS protocol) against a running backend.
//
//	go run ./scripts/soak -url ws://localhost:8089/ws
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/synctune/backend/office"
)

type envelope struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

type client struct {
	name string
	id   string
	x, y float64
	conn *websocket.Conn
	mu   sync.Mutex
	inbox []map[string]any
	done  chan struct{}
}

func dial(url, name string) (*client, error) {
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	cl := &client{name: name, conn: c, done: make(chan struct{}), x: office.SpawnX, y: office.SpawnY}
	go func() {
		defer close(cl.done)
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var env envelope
			if json.Unmarshal(data, &env) != nil {
				continue
			}
			var payload map[string]any
			_ = json.Unmarshal(env.Payload, &payload)
			if payload == nil {
				payload = map[string]any{}
			}
			payload["_event"] = env.Event
			cl.mu.Lock()
			cl.inbox = append(cl.inbox, payload)
			cl.mu.Unlock()
		}
	}()
	return cl, nil
}

func (c *client) send(event string, payload any) error {
	b, _ := json.Marshal(map[string]any{"event": event, "payload": payload})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *client) close() { _ = c.conn.Close(); <-c.done }

func (c *client) wait(event string, timeout time.Duration, pred func(map[string]any) bool) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for i, msg := range c.inbox {
			if msg["_event"] == event && (pred == nil || pred(msg)) {
				c.inbox = append(c.inbox[:i], c.inbox[i+1:]...)
				c.mu.Unlock()
				return msg, nil
			}
		}
		c.mu.Unlock()
		time.Sleep(15 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s: timeout waiting for %s", c.name, event)
}

func (c *client) drain(event string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.inbox[:0]
	for _, msg := range c.inbox {
		if msg["_event"] != event {
			kept = append(kept, msg)
		}
	}
	c.inbox = kept
}

func (c *client) saw(event string, pred func(map[string]any) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.inbox {
		if msg["_event"] == event && (pred == nil || pred(msg)) {
			return true
		}
	}
	return false
}

// walkTo steps toward target under server caps (~240 px/s, min interval 66ms).
func (c *client) walkTo(x, y float64) {
	const step = 16.0
	const pause = 100 * time.Millisecond
	for hops := 0; hops < 200; hops++ {
		dx, dy := x-c.x, y-c.y
		dist := math.Hypot(dx, dy)
		if dist <= step {
			c.x, c.y = x, y
			_ = c.send("presence_update", map[string]any{"x": c.x, "y": c.y, "dir": "down"})
			time.Sleep(pause)
			return
		}
		c.x += dx / dist * step
		c.y += dy / dist * step
		_ = c.send("presence_update", map[string]any{"x": c.x, "y": c.y, "dir": "down"})
		time.Sleep(pause)
	}
}

func (c *client) walkVia(points ...[2]float64) {
	for _, p := range points {
		c.walkTo(p[0], p[1])
	}
}

// leaveMeeting exits via the zone door to the open corridor (spawn row).
func (c *client) leaveMeeting(m *office.OfficeMap, zoneID string) {
	outX, outY, inX, inY := office.MeetingEntryPath(m, zoneID)
	// Axis L to door-inside, then south through door.
	c.walkVia([2]float64{c.x, inY}, [2]float64{inX, inY}, [2]float64{outX, outY})
}

// enterMeeting walks corridor → door-outside → door-inside → optional deeper floor (map_v2).
func (c *client) enterMeeting(m *office.OfficeMap, zoneID string, floorX, floorY float64) {
	outX, outY, inX, inY := office.MeetingEntryPath(m, zoneID)
	// Reach door via spawn-row corridor (avoids cutting through walls from another room).
	c.walkVia([2]float64{c.x, outY}, [2]float64{outX, outY}, [2]float64{inX, inY})
	if floorX == inX && floorY == inY {
		return
	}
	// Axis-aligned: stay on clear bottom floor row then north (avoids meeting desks).
	c.walkVia([2]float64{floorX, inY}, [2]float64{floorX, floorY})
}

func check(ok bool, name string, fails *[]string) {
	if ok {
		fmt.Printf("  PASS  %s\n", name)
		return
	}
	fmt.Printf("  FAIL  %s\n", name)
	*fails = append(*fails, name)
}

func presenceIDsByUser(rj map[string]any, username string) []string {
	var out []string
	arr, _ := rj["presence_state"].([]any)
	for _, raw := range arr {
		m, _ := raw.(map[string]any)
		if m["username"] == username {
			if id, ok := m["connection_id"].(string); ok && id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func meetVoicePred(zoneID string) func(map[string]any) bool {
	needle := ":meet:" + zoneID
	return func(p map[string]any) bool {
		gid, _ := p["group_id"].(string)
		tok, _ := p["token"].(string)
		return strings.Contains(gid, needle) && tok != ""
	}
}

func main() {
	url := flag.String("url", "ws://localhost:8089/ws", "websocket url")
	room := flag.String("room", fmt.Sprintf("%06d", time.Now().Unix()%1000000), "room id (6 digits)")
	flag.Parse()

	m := office.DefaultMap()
	openX, openY := office.SpawnX, office.SpawnY
	// Clear floor tiles inside each meeting (avoid desk-blocked MeetingCenter diagonals).
	// meeting-a tip (1,1)→(48,48); door-adjacent floor (208,240). meeting-b tip (12,1)→(400,48).
	meetAFloorX, meetAFloorY := 208.0, 240.0
	meetBFloorX, meetBFloorY := 400.0, 48.0
	// Open-office desk (tx=4,ty=10) with neighbor on spawn row — FindTileCenter-style solid probe.
	deskOutX, deskOutY, deskX, deskY := 144.0, 304.0, 144.0, 336.0

	fmt.Printf("Soak against %s room=%s (map_v2 spawn=%.0f,%.0f)\n", *url, *room, openX, openY)
	fails := []string{}
	usedIDs := map[string]bool{}

	join := func(name string) *client {
		cl, err := dial(*url, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dial %s: %v\n", name, err)
			os.Exit(2)
		}
		_ = cl.send("join", map[string]any{"username": name, "room_id": *room})
		rj, err := cl.wait("room_joined", 5*time.Second, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s join: %v\n", name, err)
			os.Exit(2)
		}
		for _, id := range presenceIDsByUser(rj, name) {
			if !usedIDs[id] {
				cl.id = id
				usedIDs[id] = true
				break
			}
		}
		cl.x, cl.y = openX, openY
		return cl
	}

	a := join("Alice")
	a2 := join("Alice")
	b := join("Bob")
	c := join("Carol")
	d := join("Dave")
	defer a.close()
	defer a2.close()
	defer b.close()
	defer c.close()
	defer d.close()

	for _, cl := range []*client{a, a2, b, c, d} {
		cl.drain("voice_credentials")
	}

	aliceCount := 0
	scout, err := dial(*url, "Scout2")
	if err == nil {
		_ = scout.send("join", map[string]any{"username": "Scout2", "room_id": *room})
		rj, err := scout.wait("room_joined", 5*time.Second, nil)
		if err == nil {
			aliceCount = len(presenceIDsByUser(rj, "Alice"))
		}
		scout.close()
	}
	check(aliceCount >= 2, fmt.Sprintf("two tabs = two Alice presences (got %d)", aliceCount), &fails)
	check(a.id != "" && a2.id != "" && a.id != a2.id, "Alice tabs have distinct connection_ids", &fails)

	// Meeting-a voice + chat (door entry → interior floor)
	a.drain("voice_credentials")
	b.drain("voice_credentials")
	c.drain("voice_credentials")
	a.enterMeeting(m, "meeting-a", meetAFloorX, meetAFloorY)
	b.enterMeeting(m, "meeting-a", 48, 48) // tip floor tile (1,1)
	c.walkTo(openX, openY)

	_, errA := a.wait("voice_credentials", 6*time.Second, meetVoicePred("meeting-a"))
	_, errB := b.wait("voice_credentials", 6*time.Second, meetVoicePred("meeting-a"))
	check(errA == nil && errB == nil, "meeting-a voice credentials for A+B", &fails)
	time.Sleep(250 * time.Millisecond)
	check(!c.saw("voice_credentials", meetVoicePred("meeting-a")), "C outside meeting-a has no meeting voice", &fails)

	_ = a.send("meeting_send", map[string]any{"text": "hello meeting-a"})
	_, err = b.wait("meeting_message", 3*time.Second, func(p map[string]any) bool {
		return p["text"] == "hello meeting-a"
	})
	check(err == nil, "meeting-a chat A→B", &fails)
	time.Sleep(150 * time.Millisecond)
	check(!c.saw("meeting_message", nil), "C outside does not receive meeting-a chat", &fails)

	// Meeting-b (second room on map_v2) — exit meeting-a via door first
	a.drain("voice_credentials")
	b.drain("voice_credentials")
	a.leaveMeeting(m, "meeting-a")
	b.leaveMeeting(m, "meeting-a")
	a.enterMeeting(m, "meeting-b", meetBFloorX, meetBFloorY)
	b.enterMeeting(m, "meeting-b", 432, 80)
	_, errA = a.wait("voice_credentials", 8*time.Second, meetVoicePred("meeting-b"))
	_, errB = b.wait("voice_credentials", 8*time.Second, meetVoicePred("meeting-b"))
	check(errA == nil && errB == nil, "meeting-b voice credentials for A+B", &fails)

	// Bubble from open floor (spawn)
	a.walkTo(openX, openY)
	b.walkTo(openX+40, openY)
	c.walkTo(openX+80, openY)
	time.Sleep(200 * time.Millisecond)
	a.drain("voice_credentials")
	b.drain("voice_credentials")
	c.drain("voice_credentials")

	check(c.id != "", "Carol connection_id known", &fails)
	_ = a.send("bubble_invite", map[string]any{"to_connection_id": c.id})
	var inv map[string]any
	inv, err = c.wait("bubble_invite", 3*time.Second, nil)
	check(err == nil, "bubble invite C", &fails)
	bid, _ := inv["bubble_id"].(string)
	_ = c.send("bubble_accept", map[string]any{"bubble_id": bid})

	_, errA = a.wait("voice_credentials", 6*time.Second, func(p map[string]any) bool {
		gid, _ := p["group_id"].(string)
		tok, _ := p["token"].(string)
		return strings.Contains(gid, ":bubble:") && tok != ""
	})
	var errC error
	_, errC = c.wait("voice_credentials", 6*time.Second, func(p map[string]any) bool {
		gid, _ := p["group_id"].(string)
		tok, _ := p["token"].(string)
		return strings.Contains(gid, ":bubble:") && tok != ""
	})
	check(errA == nil && errC == nil, "bubble voice credentials for A+C", &fails)
	time.Sleep(200 * time.Millisecond)
	check(!b.saw("voice_credentials", func(p map[string]any) bool {
		gid, _ := p["group_id"].(string)
		tok, _ := p["token"].(string)
		return strings.Contains(gid, ":bubble:") && tok != ""
	}), "B outside bubble has no bubble voice", &fails)

	a.drain("voice_credentials")
	_ = a.send("bubble_leave", map[string]any{})
	_, err = a.wait("voice_credentials", 4*time.Second, func(p map[string]any) bool {
		gid, _ := p["group_id"].(string)
		return !strings.Contains(gid, ":bubble:")
	})
	check(err == nil, "A leave bubble clears bubble voice", &fails)

	// Desk collision (map_v2 solid desk — replaces private spoof)
	a.walkVia([2]float64{openX, openY}, [2]float64{deskOutX, deskOutY})
	b.walkVia([2]float64{openX, openY}, [2]float64{deskOutX, deskOutY})
	time.Sleep(120 * time.Millisecond)
	b.drain("presence_corrected")
	b.walkTo(deskX, deskY)
	_, err = b.wait("presence_corrected", 3*time.Second, nil)
	check(err == nil, "desk collision rejected (presence_corrected)", &fails)
	b.x, b.y = deskOutX, deskOutY

	// Follow
	a.walkTo(openX, openY)
	d.walkTo(openX, openY+50)
	d.drain("follow_updated")
	_ = d.send("follow_start", map[string]any{"target_connection_id": a.id})
	_, err = d.wait("follow_updated", 2*time.Second, func(p map[string]any) bool {
		return p["following_id"] == a.id
	})
	check(err == nil, "follow_start D→A", &fails)

	// Bell
	_ = a.send("bell_ring", map[string]any{"target_connection_id": d.id})
	_, err = d.wait("bell_ring", 2*time.Second, nil)
	check(err == nil, "bell_ring delivered", &fails)

	// Music queue
	b.drain("queue_updated")
	_ = a.send("add_song", map[string]any{
		"video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"added_by":  "Alice",
	})
	_, err = b.wait("queue_updated", 8*time.Second, nil)
	check(err == nil, "music queue sync (add_song)", &fails)

	fmt.Println()
	if len(fails) == 0 {
		fmt.Println("SOAK PROTOCOL: ALL CHECKS PASSED")
		fmt.Println("Human glance still useful: mic audio in Chrome + PeerConnection cleanup.")
		os.Exit(0)
	}
	fmt.Printf("SOAK PROTOCOL: %d FAILED\n", len(fails))
	for _, f := range fails {
		fmt.Printf("  - %s\n", f)
	}
	os.Exit(1)
}
