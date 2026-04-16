# CLAUDE.md — synctune-backend
## Go Backend · net/http + Melody + Redis

อ่านไฟล์นี้ก่อนทำงานใดๆ ใน repo นี้เสมอ

---

## 1. Stack และโครงสร้าง

```
synctune-backend/
├── CLAUDE.md
├── docs/                  ← Git Submodule (synctune-docs)
├── main.go                ← Entry point + daily cleanup goroutine
├── go.mod
├── .env.example
├── Dockerfile
├── config/config.go       ← โหลด ENV Variables
├── model/
│   ├── playlist.go        ← Song, PlaylistState, WSMessage structs
│   ├── user.go            ← User, ChatMessage structs
│   └── errors.go          ← Sentinel Errors ทั้งหมด
├── store/redis.go         ← Redis operations per-room + FlushAll
├── hub/hub.go             ← WebSocket connection pool + multi-room routing
├── controller/
│   ├── queue.go           ← Business logic: add/remove/reorder/skip/song_ended/playback_mode
│   └── chat.go            ← Business logic: join (room) / send_message
├── broadcaster/           ← Broadcast helpers (ทุกฟังก์ชันรับ roomID)
├── ticker/seekticker.go   ← seek_sync Goroutine ทุก 5 วิ (per-room)
└── youtube/metadata.go    ← ดึง Title + Thumbnail ผ่าน oEmbed API
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

- ห้องถูกสร้างอัตโนมัติเมื่อมี Client join
- ห้องถูกลบทันทีเมื่อ Client คนสุดท้ายในห้อง disconnect (`hub.Unregister` → `store.DeleteRoom`)
- **Daily Cleanup:** ทุกวัน 06:00 Asia/Bangkok (`startDailyCleanup` ใน main.go) จะ SCAN+DEL ทุก key ที่ขึ้นต้นด้วย `synctune:room:*`

---

## 5. WebSocket Events ที่ Backend รับผิดชอบ

### รับจาก Client
| Event | Handler | Payload | หมายเหตุ |
|---|---|---|---|
| `join` | `controller.HandleJoin` | `{ username, profile_img?, room_id? }` | ต้องส่งก่อน event อื่นทุกตัว — ถ้าไม่ส่ง room_id จะสร้างห้องใหม่ให้ |
| `add_song` | `controller.HandleAddSong` | `{ youtube_url, added_by }` | ต้อง join ก่อน |
| `remove_song` | `controller.HandleRemoveSong` | `{ song_id }` (queue_id) | ต้อง join ก่อน |
| `reorder_queue` | `controller.HandleReorderQueue` | `{ song_id, new_index }` (queue_id) | ต้อง join ก่อน |
| `report_error` | `controller.HandleReportError` | `{ song_id, error_code }` (101/150) | ต้อง join ก่อน |
| `song_ended` | `controller.HandleSongEnded` | `{ song_id }` (queue_id) | ดู autoplay/shuffle/random_play |
| `skip_song` | `controller.HandleSkipSong` | `{ song_id }` (queue_id) | เล่นต่อเสมอถ้าคิวไม่ว่าง |
| `set_playback_mode` | `controller.HandleSetPlaybackMode` | `{ autoplay?, shuffle?, random_play? }` | merge ไม่ replace |
| `send_message` | `controller.HandleSendMessage` | `{ text }` | ต้อง join ก่อน, max 500 ตัวอักษร |

### ส่งไปยัง Client
| Event | เมื่อไหร่ | หมายเหตุ |
|---|---|---|
| `room_joined` | หลัง join สำเร็จ (เฉพาะ Client นั้น ไม่ broadcast) | รวม room_id, queue, history, chat_history, online_users |
| `queue_updated` | คิวเปลี่ยนแปลง (broadcast ในห้อง) | รวม history ด้วยเสมอ |
| `seek_sync` | ทุก 5 วิ ขณะ is_playing=true | per-room |
| `song_skipped` | ข้ามเพลง (broadcast ในห้อง) | reason: user_skipped / embed_not_allowed / embed_not_allowed_by_request |
| `playback_mode_updated` | หลัง set_playback_mode สำเร็จ (broadcast ในห้อง) | |
| `user_joined` | หลัง join สำเร็จ (broadcast ในห้อง) | รวม online_users ล่าสุด |
| `user_left` | client disconnect หลัง join แล้ว (broadcast ในห้อง) | รวม online_users ล่าสุด |
| `message_received` | หลัง send_message สำเร็จ (broadcast ในห้อง) | |
| `error` | ส่งกลับ Client ที่ทำ action ผิด (ไม่ broadcast) | |

---

## 6. Data Structs หลัก

```go
type Song struct {
    QueueID   string `json:"queue_id"`  // UUID ต่อ queue slot — ใช้ใน event ทุกตัว
    ID        string `json:"id"`        // YouTube Video ID (11 chars)
    Title     string `json:"title"`     // ได้จาก oEmbed API
    Thumbnail string `json:"thumbnail"` // maxresdefault.jpg หรือ hqdefault.jpg
    AddedBy   string `json:"added_by"`
    Duration  int    `json:"duration"`
}

type User struct {
    ID         string `json:"id"`          // UUID ต่อ session (= Client.ID)
    Username   string `json:"username"`
    ProfileImg string `json:"profile_img"`
}

type ChatMessage struct {
    ID        string `json:"id"`        // UUID ต่อข้อความ
    User      User   `json:"user"`
    Text      string `json:"text"`
    Timestamp int64  `json:"timestamp"` // Unix milliseconds
}
```

**สำคัญ:** `song_id` ในทุก event = `queue_id` ไม่ใช่ YouTube Video ID

---

## 7. Business Logic — Critical (อย่าเปลี่ยนโดยไม่อ่าน Spec)

### Multi-Room
- `Client.RoomID` ว่างเปล่าจนกว่าจะ `join` — ทุก handler ตรวจ `requireJoined` ก่อนเสมอ
- `hub.rooms` เป็น `map[roomID]map[clientID]*Client` — Broadcast ส่งเฉพาะ Client ในห้องเดียวกัน
- ห้องถูกลบจาก `hub.rooms` และ Redis ทันทีเมื่อ Client คนสุดท้ายออก
- Room ID: ตัวเลข 6 หลัก (100000–999999) สุ่มด้วย `crypto/rand`

### Deduplication (report_error / skip_song / song_ended)
ต้อง reload State จาก Redis ก่อนตรวจ `queue_id` เสมอ — ห้ามใช้ in-memory

### song_ended — Autoplay / Shuffle / Random
```
autoplay=false → หยุด
autoplay=true + queue ว่าง → หยุด
autoplay=true + random_play=true → สุ่ม index ใหม่
autoplay=true + shuffle=true → เล่นตาม queue ที่ shuffle ไว้แล้ว
autoplay=true (ปกติ) → เล่น index ถัดไป
```

### skip_song — เล่นต่อเสมอถ้ามีเพลงเหลือ
```
queue ว่าง → หยุด
queue มีเพลง + random_play=true → สุ่ม index ใหม่
queue มีเพลง → เล่น index ถัดไป
```
(ไม่ดู autoplay เพราะ skip = user ตั้งใจเปลี่ยน)

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
case <-t.stopCh: // ← ขาดนี้ = goroutine leak
    return
}
```

### Hub Client ID
ใช้ UUID (ไม่ใช่ RemoteAddr) เก็บใน `session.Set("client_id", ...)` ตอน Register

### Migration Guard (store/redis.go)
songs เก่าที่ไม่มี `queue_id` จะถูก backfill เป็น `videoID_index` อัตโนมัติใน `GetState`
