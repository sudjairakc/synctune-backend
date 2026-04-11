# SyncTune Backend

Go backend สำหรับ SyncTune — Real-time Office Jukebox

**Stack:** Go · net/http · Melody (WebSocket) · Redis · zerolog

---

## Prerequisites

- Go 1.22+
- Docker (สำหรับ Redis)

---

## เริ่มต้นใช้งาน

### 1. รัน Redis

```bash
docker run -d --name synctune-redis -p 6379:6379 redis:alpine
```

### 2. ตั้งค่า Environment

```bash
cp .env.example .env
```

### 3. ดาวน์โหลด Dependencies

```bash
go mod tidy
```

### 4. รัน Backend

```bash
go run main.go
```

Backend จะรันที่ `http://localhost:8080`

---

## คำสั่งที่ใช้บ่อย

```bash
# Hot reload (ต้องติดตั้ง air ก่อน: go install github.com/air-verse/air@latest)
air

# รันปกติ
go run main.go

# Debug (log ละเอียด)
LOG_LEVEL=debug go run main.go

# Build binary
go build -o synctune-backend .

# Unit tests
go test ./... -v

# Unit tests พร้อม Coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Integration tests (ต้องมี Docker)
go test ./... -tags=integration -v

# Lint
golangci-lint run

# Format
gofmt -w .
```

---

## Environment Variables

| Variable | Default | คำอธิบาย |
|---|---|---|
| `PORT` | `8080` | Port ที่ Backend รัน |
| `REDIS_URL` | `localhost:6379` | Redis connection URL |
| `SEEK_BROADCAST_INTERVAL` | `5` | ช่วงเวลา seek_sync (วินาที) |
| `MAX_QUEUE_SIZE` | `100` | จำนวนเพลงสูงสุดในคิว |
| `RATE_LIMIT_ADD_SONG` | `10` | จำนวน add_song สูงสุดต่อนาที/client |
| `LOG_LEVEL` | `info` | ระดับ Log (debug/info/warn/error) |

---

## API Endpoints

| Method | Path | คำอธิบาย |
|---|---|---|
| `GET` | `/ws` | WebSocket endpoint |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Metrics (connections, queue size) |

```bash
curl http://localhost:8080/health
curl http://localhost:8080/metrics
```

---

## WebSocket Events

### Client → Server

| Event | Payload | คำอธิบาย |
|---|---|---|
| `join` | `{ username, profile_img? }` | ส่งทันทีหลัง connect ก่อน event อื่น |
| `add_song` | `{ youtube_url, added_by }` | เพิ่มเพลงลงคิว (`added_by` ถูก ignore ถ้า join แล้ว) |
| `remove_song` | `{ song_id }` | ลบเพลงออกจากคิว |
| `reorder_queue` | `{ song_id, new_index }` | เปลี่ยนลำดับเพลง |
| `report_error` | `{ song_id, error_code }` | แจ้ง YouTube Error 101/150 |
| `song_ended` | `{ song_id }` | เพลงจบ — server advance queue ตาม autoplay/shuffle/random |
| `skip_song` | `{ song_id }` | ข้ามเพลงปัจจุบัน |
| `set_playback_mode` | `{ autoplay?, shuffle?, random_play? }` | เปลี่ยน playback mode (merge ไม่ replace) |
| `send_message` | `{ text }` | ส่งข้อความแชท (ต้อง join ก่อน) |

> `song_id` ในทุก event หมายถึง `queue_id` (UUID) ไม่ใช่ YouTube Video ID

### Server → Client

| Event | Payload | คำอธิบาย |
|---|---|---|
| `initial_state` | queue, index, seek, is_playing, autoplay, shuffle, random_play, history, chat_history, online_users | ส่งให้ Client ใหม่เมื่อ connect |
| `queue_updated` | queue, index, is_playing, history | Broadcast เมื่อคิวเปลี่ยน |
| `seek_sync` | `{ seek_time, is_playing }` | Broadcast ทุก 5 วิ ขณะกำลังเล่น |
| `song_skipped` | `{ song_id, title, reason, error_code }` | Broadcast เมื่อข้ามเพลง |
| `playback_mode_updated` | `{ autoplay, shuffle, random_play }` | Broadcast เมื่อ mode เปลี่ยน |
| `user_joined` | `{ user, online_users }` | Broadcast เมื่อมีคนเข้าร่วม |
| `user_left` | `{ user, online_users }` | Broadcast เมื่อมีคน disconnect |
| `message_received` | `{ id, user, text, timestamp }` | Broadcast ข้อความแชทใหม่ |
| `error` | `{ code, message }` | ส่งกลับ Client ที่ทำ action ผิด |

#### song_skipped — reason values
| reason | ความหมาย |
|---|---|
| `user_skipped` | ผู้ใช้กดข้าม |
| `embed_not_allowed` | YouTube Error 101 |
| `embed_not_allowed_by_request` | YouTube Error 150 |

#### error — code values
| code | สาเหตุ |
|---|---|
| `NOT_JOINED` | ส่ง event อื่นก่อน `join` |
| `INVALID_USERNAME` | username ว่าง |
| `EMPTY_MESSAGE` | text ว่าง |
| `RATE_LIMITED` | ส่งเกิน rate limit |
| `DUPLICATE_SONG` | เพลงซ้ำใน queue (ปิดอยู่) |
| `QUEUE_FULL` | queue เต็ม (max 100) |
| `INVALID_URL` | YouTube URL ไม่ถูกต้อง |
| `SONG_NOT_FOUND` | ไม่พบ song_id ใน queue |
| `INVALID_PLAYBACK_MODE` | เปิด shuffle + random_play พร้อมกัน |
| `SERVER_ERROR` | ข้อผิดพลาดภายใน |

---

## Data Objects

### Song
```json
{
  "queue_id": "uuid-per-slot",
  "id": "YouTubeVideoID",
  "title": "ชื่อเพลง",
  "thumbnail": "https://i.ytimg.com/vi/.../maxresdefault.jpg",
  "added_by": "ชื่อผู้เพิ่ม",
  "duration": 0
}
```

Title และ Thumbnail ดึงอัตโนมัติจาก YouTube oEmbed API เมื่อ `add_song`

### User
```json
{
  "id": "uuid-per-session",
  "username": "Alice",
  "profile_img": "https://..."
}
```

`id` เปลี่ยนทุกครั้งที่ reconnect

### ChatMessage
```json
{
  "id": "uuid",
  "user": { "id": "...", "username": "Alice", "profile_img": "..." },
  "text": "ขอเพิ่มเพลงหน่อย",
  "timestamp": 1744299811000
}
```

---

## Daily Cleanup

ทุกวัน **06:00 Asia/Bangkok** backend จะล้าง Redis keys ทั้งหมด (`synctune:state`, `synctune:history`, `synctune:chat`) โดยอัตโนมัติผ่าน goroutine `startDailyCleanup` ใน `main.go`

---

## โครงสร้างโปรเจ็กต์

```
synctune-backend/
├── main.go                    ← Entry point + daily cleanup goroutine
├── config/config.go           ← โหลด ENV Variables
├── model/
│   ├── playlist.go            ← Song, PlaylistState, WSMessage structs
│   ├── user.go                ← User, ChatMessage structs
│   └── errors.go              ← Sentinel errors
├── store/redis.go             ← Redis operations (state/history/chat) + FlushAll
├── hub/hub.go                 ← WebSocket connection pool + User/ChatLimiter
├── controller/
│   ├── queue.go               ← Queue + playback business logic
│   └── chat.go                ← Chat + user join business logic
├── broadcaster/broadcaster.go ← Broadcast helper functions
├── ticker/seekticker.go       ← seek_sync goroutine
└── youtube/metadata.go        ← oEmbed API + thumbnail fallback
```

---

## รัน Docker

```bash
# Build image
docker build -t synctune-backend .

# รันพร้อม Redis
docker run -d --name synctune-redis -p 6379:6379 redis:alpine
docker run -d \
  --name synctune-backend \
  -p 8080:8080 \
  -e REDIS_URL=synctune-redis:6379 \
  --link synctune-redis \
  synctune-backend
```
