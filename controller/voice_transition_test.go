package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/synctune/backend/hub"
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
				// Place adjacent to meeting-a, then walk in (speed-legal).
				client.LastX, client.LastY = 900, 256
				client.LastZoneID = ""
				client.LastPresenceAt = time.Now().Add(-time.Second)
				payload, _ := json.Marshal(presenceUpdatePayload{X: 980, Y: 256, Dir: "right"})
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
	// spawn is open → no voice emit
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

	cases := []struct {
		name     string
		bubbleID string
		x, y     float64
		want     string
	}{
		{"open silent", "", office.SpawnX, office.SpawnY, ""},
		{"meeting", "", 1088, 256, "st:160002:meet:meeting-a"},
		{"bubble wins", "bub-1", 1088, 256, "st:160002:bubble:bub-1"},
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
