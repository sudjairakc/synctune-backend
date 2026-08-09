package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

func withLiveKitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LIVEKIT_URL", "wss://lk.test")
	t.Setenv("LIVEKIT_API_KEY", "devkey")
	t.Setenv("LIVEKIT_API_SECRET", "secret")
}

func waitVoiceCredentials(t *testing.T, ch <-chan map[string]interface{}, wantGroup string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	return waitCollectedEvent(t, ch, "voice_credentials", timeout, func(p map[string]interface{}) bool {
		gid, _ := p["group_id"].(string)
		return gid == wantGroup
	})
}

func countVoiceCredentials(ch <-chan map[string]interface{}, drain time.Duration) int {
	n := 0
	deadline := time.After(drain)
	for {
		select {
		case <-deadline:
			return n
		case msg, ok := <-ch:
			if !ok {
				return n
			}
			if msg["event"] == "voice_credentials" {
				n++
			}
		}
	}
}

func TestVoiceTransitions_Table(t *testing.T) {
	withLiveKitEnv(t)

	const roomID = "160001"

	type step struct {
		name      string
		apply     func(t *testing.T, h *hub.Hub, client *hub.Client, peer *hub.Client)
		wantGroup string // expected ActiveVoiceGroup + emitted group_id ("" = clear)
		wantEmit  bool
	}

	// Shared peer for bubble invite/accept steps.
	steps := []step{
		{
			name: "open→meeting",
			apply: func(t *testing.T, h *hub.Hub, client *hub.Client, _ *hub.Client) {
				m := office.DefaultMap()
				outX, outY, inX, inY := office.MeetingEntryPath(m, "meeting-a")
				client.LastX, client.LastY = outX, outY
				client.LastZoneID = ""
				client.LastPresenceAt = time.Now().Add(-time.Second)
				payload, _ := json.Marshal(presenceUpdatePayload{X: inX, Y: inY, Dir: "up"})
				HandlePresenceUpdate(h, client, payload)
			},
			wantGroup: "st:160001:meet:meeting-a",
			wantEmit:  true,
		},
		{
			name: "meeting→bubble",
			apply: func(t *testing.T, h *hub.Hub, client *hub.Client, peer *hub.Client) {
				invite, _ := json.Marshal(bubbleInvitePayload{ToConnectionID: peer.ID})
				HandleBubbleInvite(h, client, invite)
				// Inviter already in bubble after create; peer accepts for realism.
				bID := client.BubbleID
				if bID == "" {
					t.Fatal("expected bubble after invite create")
				}
				accept, _ := json.Marshal(bubbleAcceptPayload{BubbleID: bID})
				HandleBubbleAccept(h, peer, accept)
			},
			wantGroup: "", // filled dynamically from client.BubbleID
			wantEmit:  true,
		},
		{
			name: "bubble leave→meeting",
			apply: func(t *testing.T, h *hub.Hub, client *hub.Client, _ *hub.Client) {
				HandleBubbleLeave(h, client, nil)
			},
			wantGroup: "st:160001:meet:meeting-a",
			wantEmit:  true,
		},
		{
			name: "disconnect clear",
			apply: func(t *testing.T, h *hub.Hub, client *hub.Client, _ *hub.Client) {
				ClearVoiceOnDisconnect(h, client)
			},
			wantGroup: "",
			wantEmit:  true,
		},
	}

	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()

	sess, conn, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	clientID, _ := sess.Get("client_id")
	client := h.GetClient(clientID.(string))
	inbox := collectWSEvents(conn)
	joinRoom(t, h, client, roomID, "alice")
	_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)
	// Join force-sync at open spawn → clear credentials
	_ = waitVoiceCredentials(t, inbox, "", 2*time.Second)
	if client.ActiveVoiceGroup != "" {
		t.Fatalf("after join want empty ActiveVoiceGroup, got %q", client.ActiveVoiceGroup)
	}

	peer, peerInbox, cleanupPeer := registerJoined(t, h, roomID, "bob")
	defer cleanupPeer()
	_ = peerInbox // may receive bubble_invite

	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			prev := client.ActiveVoiceGroup
			st.apply(t, h, client, peer)

			want := st.wantGroup
			if st.name == "meeting→bubble" {
				if client.BubbleID == "" {
					t.Fatal("missing BubbleID after meeting→bubble")
				}
				want = "st:" + roomID + ":bubble:" + client.BubbleID
			}

			if client.ActiveVoiceGroup != want {
				t.Fatalf("ActiveVoiceGroup = %q, want %q (prev %q)", client.ActiveVoiceGroup, want, prev)
			}

			if st.wantEmit {
				payload := waitVoiceCredentials(t, inbox, want, 2*time.Second)
				if want != "" {
					if token, _ := payload["token"].(string); token == "" {
						t.Fatal("expected non-empty token for voice group")
					}
					if url, _ := payload["url"].(string); url == "" {
						t.Fatal("expected non-empty url")
					}
				} else {
					if token, _ := payload["token"].(string); token != "" {
						t.Fatalf("clear should have empty token, got %q", token)
					}
				}
			}

			// After disconnect clear, social state still implies a group — do not re-sync.
			if st.name == "disconnect clear" {
				return
			}

			// Redundant sync must not re-emit.
			before := client.ActiveVoiceGroup
			SyncActiveVoice(h, client)
			if n := countVoiceCredentials(inbox, 80*time.Millisecond); n != 0 {
				t.Fatalf("redundant SyncActiveVoice emitted %d voice_credentials", n)
			}
			if client.ActiveVoiceGroup != before {
				t.Fatalf("ActiveVoiceGroup changed on no-op sync: %q → %q", before, client.ActiveVoiceGroup)
			}
		})
	}
}

func TestSyncActiveVoice_UsesDeriveActiveVoiceGroup(t *testing.T) {
	withLiveKitEnv(t)

	m := office.DefaultMap()
	ax, ay := office.MeetingCenter(m, "meeting-a")
	bx, by := office.MeetingCenter(m, "meeting-b")
	cases := []struct {
		name     string
		bubbleID string
		x, y     float64
		want     string
	}{
		{"open silent", "", office.SpawnX, office.SpawnY, ""},
		{"meeting", "", ax, ay, "st:160002:meet:meeting-a"},
		{"meeting-b", "", bx, by, "st:160002:meet:meeting-b"},
		{"bubble wins", "bub-1", ax, ay, "st:160002:bubble:bub-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeStore()
			h := hub.NewHub(fs)
			go h.Run()
			sess, conn, cleanup := dialTestSession(t)
			defer cleanup()
			h.Register(sess)
			id, _ := sess.Get("client_id")
			client := h.GetClient(id.(string))
			inbox := collectWSEvents(conn)
			joinRoom(t, h, client, "160002", "carol")
			_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)
			// Drain join force-sync clear
			_ = waitVoiceCredentials(t, inbox, "", 2*time.Second)

			client.LastX, client.LastY = tc.x, tc.y
			client.BubbleID = tc.bubbleID
			zoneID, _ := office.DefaultMap().ZoneAt(tc.x, tc.y)
			client.LastZoneID = zoneID

			SyncActiveVoice(h, client)
			if client.ActiveVoiceGroup != tc.want {
				t.Fatalf("ActiveVoiceGroup = %q, want %q", client.ActiveVoiceGroup, tc.want)
			}
			if tc.want == "" {
				if n := countVoiceCredentials(inbox, 80*time.Millisecond); n != 0 {
					t.Fatalf("open zone should not emit voice_credentials, got %d", n)
				}
				return
			}
			_ = waitVoiceCredentials(t, inbox, tc.want, 2*time.Second)
		})
	}
}

// Mint failure must leave ActiveVoiceGroup empty so a later sync can remint.
func TestSyncActiveVoice_MintFailThenRetryWhenConfigured(t *testing.T) {
	// Ensure LiveKit env is unset for the fail path.
	t.Setenv("LIVEKIT_URL", "")
	t.Setenv("LIVEKIT_API_KEY", "")
	t.Setenv("LIVEKIT_API_SECRET", "")

	const roomID = "160003"
	wantGroup := "st:" + roomID + ":meet:meeting-a"

	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()
	sess, conn, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	id, _ := sess.Get("client_id")
	client := h.GetClient(id.(string))
	inbox := collectWSEvents(conn)
	joinRoom(t, h, client, roomID, "dave")
	_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)
	// Drain join force-sync clear (open spawn)
	_ = waitVoiceCredentials(t, inbox, "", 2*time.Second)

	ax, ay := office.MeetingCenter(office.DefaultMap(), "meeting-a")
	client.LastX, client.LastY = ax, ay
	client.LastZoneID = "meeting-a"

	SyncActiveVoice(h, client)
	// Mint fail keeps group_id in payload (FE "unavailable") but ActiveVoiceGroup stays empty.
	failPayload := waitVoiceCredentials(t, inbox, wantGroup, 2*time.Second)
	if token, _ := failPayload["token"].(string); token != "" {
		t.Fatalf("mint fail should emit empty token, got %q", token)
	}
	if client.ActiveVoiceGroup != "" {
		t.Fatalf("after mint fail ActiveVoiceGroup = %q, want empty (so retry can remint)", client.ActiveVoiceGroup)
	}

	// Configure LiveKit and sync again — must emit real credentials.
	withLiveKitEnv(t)
	SyncActiveVoice(h, client)
	payload := waitVoiceCredentials(t, inbox, wantGroup, 2*time.Second)
	if client.ActiveVoiceGroup != wantGroup {
		t.Fatalf("after retry ActiveVoiceGroup = %q, want %q", client.ActiveVoiceGroup, wantGroup)
	}
	if token, _ := payload["token"].(string); token == "" {
		t.Fatal("expected non-empty token after remint")
	}
}

// TestHandleJoin_EmitsVoiceClearAtSpawn — reconnect matrix: join at spawn force-syncs clear.
func TestHandleJoin_EmitsVoiceClearAtSpawn(t *testing.T) {
	withLiveKitEnv(t)
	const roomID = "160018"

	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()
	sess, conn, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	id, _ := sess.Get("client_id")
	client := h.GetClient(id.(string))
	inbox := collectWSEvents(conn)

	joinRoom(t, h, client, roomID, "erin")
	_ = waitCollectedEvent(t, inbox, "room_joined", 2*time.Second, nil)
	payload := waitVoiceCredentials(t, inbox, "", 2*time.Second)
	if client.ActiveVoiceGroup != "" {
		t.Fatalf("ActiveVoiceGroup = %q, want empty after open spawn", client.ActiveVoiceGroup)
	}
	if token, _ := payload["token"].(string); token != "" {
		t.Fatalf("spawn clear should have empty token, got %q", token)
	}
}

// TestSyncActiveVoiceOnJoin_MeetingZoneAfterPresence — join-path voice sync after
// presence is set inside a meeting zone (simulates restore; production spawn is open).
func TestSyncActiveVoiceOnJoin_MeetingZoneAfterPresence(t *testing.T) {
	withLiveKitEnv(t)
	const roomID = "160019"
	want := "st:" + roomID + ":meet:meeting-a"

	fs := newFakeStore()
	h := hub.NewHub(fs)
	go h.Run()
	sess, conn, cleanup := dialTestSession(t)
	defer cleanup()
	h.Register(sess)
	id, _ := sess.Get("client_id")
	client := h.GetClient(id.(string))
	inbox := collectWSEvents(conn)

	h.SetClientRoom(client.ID, roomID)
	h.SetClientUser(client.ID, model.User{ID: client.ID, Username: "erin"})
	ax, ay := office.MeetingCenter(office.DefaultMap(), "meeting-a")
	if _, err := placePresence(h, client, roomID, ax, ay, "down"); err != nil {
		t.Fatalf("placePresence: %v", err)
	}

	SyncActiveVoiceOnJoin(h, client)
	payload := waitVoiceCredentials(t, inbox, want, 2*time.Second)
	if client.ActiveVoiceGroup != want {
		t.Fatalf("ActiveVoiceGroup = %q, want %q", client.ActiveVoiceGroup, want)
	}
	if token, _ := payload["token"].(string); token == "" {
		t.Fatal("expected non-empty token for meeting voice on join sync")
	}
	if url, _ := payload["url"].(string); url == "" {
		t.Fatal("expected non-empty url")
	}
}
