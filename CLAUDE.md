# CLAUDE.md — synctune-backend
## Go Backend · net/http + Melody + Redis · v1.0.1

อ่านไฟล์นี้ก่อนทำงานใดๆ ใน repo นี้เสมอ

---

## Behavioral Guidelines

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.


## 1. Stack และโครงสร้าง

```
synctune-backend/
├── CLAUDE.md
├── SKILL.md                   ← คู่มือ Claude Code Skills สำหรับ repo นี้
├── DESIGN.md                  ← Architecture decisions และ design notes
├── docs/                      ← Git Submodule (synctune-docs)
├── main.go                    ← Entry point + daily cleanup goroutine
├── go.mod
├── .env.example
├── Dockerfile
├── config/config.go           ← โหลด ENV Variables
├── model/
│   ├── playlist.go            ← Song, PlaylistState, SoundPad, BroadcastSchedule, TopSpender structs
│   ├── user.go                ← User, ChatMessage structs
│   ├── vote.go                ← Vote, VoteAction structs
│   └── errors.go              ← Sentinel Errors ทั้งหมด
├── store/redis.go             ← Redis operations per-room (state/history/chat/soundpad/vote)
├── hub/hub.go                 ← WebSocket connection pool + multi-room routing
├── controller/
│   ├── queue.go               ← Business logic: add/remove/reorder/skip/song_ended/playback_mode
│   ├── chat.go                ← Business logic: join (room) / send_message
│   ├── soundpad.go            ← SoundPad: set/clear/play/stop handlers
│   ├── voice.go               ← Voice PTT: WebRTC signaling relay handlers
│   └── vote.go                ← Voting: startVote / HandleVoteCast / resolveVote
├── broadcaster/               ← Broadcast helpers (ทุกฟังก์ชันรับ roomID)
├── broadcast/scheduler.go     ← Scheduled broadcast per room (cron-based)
├── ticker/seekticker.go       ← seek_sync Goroutine ทุก 5 วิ (per-room)
├── admin/admin.go             ← Admin panel + PromptPay QR + TopSpenders API
├── promptpay/promptpay.go     ← PromptPay QR generation
└── youtube/metadata.go        ← ดึง Title + Thumbnail ผ่าน oEmbed API
```

**หมายเหตุ:** ใช้ `net/http` standard library (ไม่ใช่ Fiber) เพราะ Fiber/fasthttp ไม่รองรับ WebSocket hijacking ที่ melody ต้องการ

---

## 2. คำสั่งที่ใช้บ่อย

```bash
# รัน (ต้องมี Redis ก่อน)
docker run -d --name synctune-redis -p 6379:6379 redis:alpine
go run main.go               # ไม่มี hot reload
air                          # hot reload

# Tests
go test ./... -v
go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out
go test ./... -tags=integration -v   # ต้องการ Docker (testcontainers)

# Lint + Format
golangci-lint run
gofmt -w .

# Health check
curl http://localhost:8080/health
curl http://localhost:8080/metrics

# Debug broadcast (ดู log ละเอียด)
LOG_LEVEL=debug go run main.go
```

---

## 3. Environment Variables

```dotenv
PORT=8080
REDIS_URL=localhost:6379
SEEK_BROADCAST_INTERVAL=5
MAX_QUEUE_SIZE=100
RATE_LIMIT_ADD_SONG=10
LOG_LEVEL=info
ALLOWED_ORIGINS=*
```

---

## 4. Redis Keys (Multi-Room)

| Key | ประเภท | เนื้อหา |
|---|---|---|
| `synctune:room:{roomID}:state` | String (JSON) | PlaylistState ของห้องนั้น |
| `synctune:room:{roomID}:history` | List | HistorySong[] newest first, max 50 |
| `synctune:room:{roomID}:chat` | List | ChatMessage[] newest first, max 100 |
| `synctune:room:{roomID}:soundpad` | String (JSON) | `[SoundPadSlot\|null, ...]` 50 slots |
| `synctune:room:{roomID}:soundpad_history` | List | SoundPadPlayEvent[] newest first |
| `synctune:room:{roomID}:vote` | String (JSON) | Vote ที่ active อยู่ (TTL 30 วิ) |
| `synctune:schedules` | String (JSON) | BroadcastSchedule[] ทั้งหมด |
| `synctune:top_spenders` | String (JSON) | TopSpender[] |

- ห้องถูกสร้างอัตโนมัติเมื่อมี Client join
- ห้องถูกลบทันทีเมื่อ Client คนสุดท้ายในห้อง disconnect (`hub.Unregister` → `store.DeleteRoom`)
- **Daily Cleanup:** ทุกวัน 06:00 Asia/Bangkok (`startDailyCleanup` ใน main.go) จะ SCAN+DEL ทุก key ที่ขึ้นต้นด้วย `synctune:room:*`

---

## 5. WebSocket Events ที่ Backend รับผิดชอบ

### รับจาก Client
| Event | Handler | Payload |
|---|---|---|
| `join` | `controller.HandleJoin` | `{ username, profile_img?, room_id? }` |
| `add_song` | `controller.HandleAddSong` | `{ youtube_url, added_by }` |
| `remove_song` | `controller.HandleRemoveSong` | `{ song_id }` (queue_id) |
| `reorder_queue` | `controller.HandleReorderQueue` | `{ song_id, new_index }` |
| `report_error` | `controller.HandleReportError` | `{ song_id, error_code }` (101/150) |
| `song_ended` | `controller.HandleSongEnded` | `{ song_id }` |
| `skip_song` | `controller.HandleSkipSong` | `{ song_id }` |
| `set_playback_mode` | `controller.HandleSetPlaybackMode` | `{ autoplay?, shuffle?, random_play?, playback_speed? }` |
| `send_message` | `controller.HandleSendMessage` | `{ text }` |
| `soundpad_set` | `controller.HandleSoundPadSet` | `{ slot, video_id, title }` |
| `soundpad_clear` | `controller.HandleSoundPadClear` | `{ slot }` |
| `soundpad_play` | `controller.HandleSoundPadPlay` | `{ slot }` |
| `soundpad_stop` | `controller.HandleSoundPadStop` | — |
| `vote_cast` | `controller.HandleVoteCast` | `{ vote_id }` |
| `voice_start` | `controller.HandleVoiceStart` | — |
| `voice_stop` | `controller.HandleVoiceStop` | — |
| `voice_join` | `controller.HandleVoiceJoin` | `{ to: client_id }` |
| `voice_offer` | `controller.HandleVoiceOffer` | `{ to, sdp }` |
| `voice_answer` | `controller.HandleVoiceAnswer` | `{ to, sdp }` |
| `voice_ice` | `controller.HandleVoiceICE` | `{ to, candidate }` |

### ส่งไปยัง Client
| Event | เมื่อไหร่ |
|---|---|
| `room_joined` | หลัง join สำเร็จ (เฉพาะ Client นั้น) |
| `queue_updated` | คิวเปลี่ยนแปลง (broadcast ในห้อง) |
| `seek_sync` | ทุก 5 วิ ขณะ is_playing=true |
| `song_skipped` | ข้ามเพลง (broadcast ในห้อง) |
| `playback_mode_updated` | หลัง set_playback_mode (broadcast) |
| `user_joined` | หลัง join (broadcast) |
| `user_left` | client disconnect (broadcast) |
| `message_received` | หลัง send_message (broadcast) |
| `soundpad_updated` | หลัง soundpad_set/clear (broadcast) |
| `soundpad_play` | หลัง soundpad_play (broadcast) |
| `soundpad_stop` | หลัง soundpad_stop (broadcast) |
| `vote_started` | เปิด vote ใหม่ (broadcast) |
| `vote_updated` | มี yes vote เพิ่ม (broadcast) |
| `vote_resolved` | vote สรุปผล (broadcast) |
| `voice_start` | มีคนเริ่ม PTT (broadcast) |
| `voice_stop` | PTT หยุด (broadcast) |
| `voice_join/offer/answer/ice` | Relay ระหว่าง clients (point-to-point) |
| `error` | ส่งกลับ Client ที่ทำ action ผิด |

---

## 6. Data Structs หลัก

```go
type Song struct {
    QueueID     string `json:"queue_id"`
    ID          string `json:"id"`
    Title       string `json:"title"`
    Thumbnail   string `json:"thumbnail"`
    AddedBy     string `json:"added_by"`
    Duration    int    `json:"duration"`
    IsBroadcast bool   `json:"is_broadcast,omitempty"`
    IsLive      bool   `json:"is_live,omitempty"`
}

type SoundPadSlot struct {
    VideoID string `json:"video_id"`
    Title   string `json:"title"`
}
const SoundPadSize = 50  // index 0–49

type Vote struct {
    ID            string     `json:"id"`
    Action        VoteAction `json:"action"` // "remove_song" | "skip_song"
    SongQueueID   string     `json:"song_queue_id"`
    InitiatedBy   string     `json:"initiated_by"`
    YesVoterIDs   []string   `json:"yes_voter_ids"`
    TotalAtStart  int        `json:"total_at_start"`
    ExpiresAt     int64      `json:"expires_at"` // Unix ms
}
// Required() = ceil(TotalAtStart / 2)
```

**สำคัญ:** `song_id` ในทุก event = `queue_id` ไม่ใช่ YouTube Video ID

---

## 7. Business Logic — Critical

### Multi-Room
- `Client.RoomID` ว่างเปล่าจนกว่าจะ `join`
- `hub.rooms` เป็น `map[roomID]map[clientID]*Client`
- Room ID: ตัวเลข 6 หลัก (100000–999999) สุ่มด้วย `crypto/rand`

### Deduplication (report_error / skip_song / song_ended)
ต้อง reload State จาก Redis ก่อนตรวจ `queue_id` เสมอ — ห้ามใช้ in-memory

### song_ended — Autoplay Logic
```
autoplay=false → หยุด
autoplay=true + queue ว่าง → หยุด
autoplay=true + random_play=true → สุ่ม index ใหม่
autoplay=true + shuffle=true → เล่นตาม queue ที่ shuffle ไว้
autoplay=true (ปกติ) → เล่น index ถัดไป
```

### skip_song — เล่นต่อเสมอถ้ามีเพลงเหลือ
```
queue ว่าง → หยุด
queue มีเพลง + random_play=true → สุ่ม index ใหม่
queue มีเพลง → เล่น index ถัดไป
```
ไม่ดู autoplay เพราะ skip = user ตั้งใจเปลี่ยน

### Voting — remove_song / skip_song
- ถ้า adder == initiator → execute ทันที ไม่เปิด vote
- ถ้า total_users == 1 → execute ทันที
- ถ้ามี vote active อยู่แล้ว → return `VOTE_IN_PROGRESS` error
- Vote TTL = 30 วินาที (เก็บใน Redis พร้อม TTL)
- `Required()` = `ceil(TotalAtStart / 2)` — majority vote

### SoundPad — Independent Playback
- `soundpad_play` ไม่กระทบ queue/playlist
- แต่ละ client จัดการ audio ของตัวเองหลังรับ event
- ไม่มี global soundpad state สำหรับ is_playing

### Rate Limits
| action | limit |
|---|---|
| `add_song` | 10 ครั้ง/นาที |
| `report_error` | 5 ครั้ง/นาที |
| `send_message` | 30 ครั้ง/นาที |

### CurrentIndex หลัง Remove Song
```go
if removeIdx < state.CurrentIndex {
    state.CurrentIndex--
}
```

### Goroutine ต้องมี Stop mechanism เสมอ
```go
select {
case <-ticker.C:
    t.tick()
case <-t.stopCh:
    return
}
```

### Migration Guard (store/redis.go)
songs เก่าที่ไม่มี `queue_id` จะถูก backfill เป็น `videoID_index` อัตโนมัติใน `GetState`
