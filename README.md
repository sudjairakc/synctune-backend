# SyncTune Backend

Go backend สำหรับ SyncTune — Real-time Office Jukebox (Multi-Room)

![Version](https://img.shields.io/badge/version-1.0.1-blue)

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
| `REDIS_URL` | `localhost:6379` | Redis connection URL (รองรับ `redis://` URL ด้วย) |
| `SEEK_BROADCAST_INTERVAL` | `5` | ช่วงเวลา seek_sync (วินาที) |
| `MAX_QUEUE_SIZE` | `100` | จำนวนเพลงสูงสุดในคิว |
| `RATE_LIMIT_ADD_SONG` | `10` | จำนวน add_song สูงสุดต่อนาที/client |
| `LOG_LEVEL` | `info` | ระดับ Log (debug/info/warn/error) |
| `ALLOWED_ORIGINS` | `*` | CORS origins (comma-separated) |

---

## API Endpoints

| Method | Path | คำอธิบาย |
|---|---|---|
| `GET` | `/ws` | WebSocket endpoint |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | จำนวน connections และ rooms |
| `GET` | `/admin` | Admin panel UI |

```bash
curl http://localhost:8080/health
curl http://localhost:8080/metrics
```

---

## Multi-Room

- แต่ละห้องมี Room ID เป็นตัวเลข **6 หลัก** (เช่น `483921`)
- ส่ง `join` โดยไม่ระบุ `room_id` → server สร้างห้องใหม่ให้อัตโนมัติ
- ส่ง `join` พร้อม `room_id` → เข้าห้องที่มีอยู่แล้ว
- เมื่อ Client คนสุดท้ายในห้อง disconnect → ห้องถูกลบออกจาก Redis ทันที
- Queue, History, Chat, SoundPad แยกกันคนละห้อง ไม่ปนกัน

---

## WebSocket Events

### Client → Server

| Event | Payload | คำอธิบาย |
|---|---|---|
| `join` | `{ username, profile_img?, room_id? }` | ต้องส่งก่อน event อื่นเสมอ |
| `add_song` | `{ youtube_url, added_by }` | เพิ่มเพลงลงคิว |
| `remove_song` | `{ song_id }` | ลบเพลงออกจากคิว (อาจเปิด vote ถ้าคนอื่น add) |
| `reorder_queue` | `{ song_id, new_index }` | เปลี่ยนลำดับเพลง |
| `report_error` | `{ song_id, error_code }` | แจ้ง YouTube Error 101/150 |
| `song_ended` | `{ song_id }` | เพลงจบ — server advance queue |
| `skip_song` | `{ song_id }` | ข้ามเพลงปัจจุบัน (อาจเปิด vote ถ้าคนอื่น add) |
| `set_playback_mode` | `{ autoplay?, shuffle?, random_play?, playback_speed? }` | เปลี่ยน playback mode/speed |
| `send_message` | `{ text }` | ส่งข้อความแชท |
| `soundpad_set` | `{ slot, video_id, title }` | ตั้งค่า SoundPad slot (0–49) |
| `soundpad_clear` | `{ slot }` | ล้าง SoundPad slot |
| `soundpad_play` | `{ slot }` | เล่นเสียงจาก slot (broadcast ทั้งห้อง) |
| `soundpad_stop` | — | หยุดเสียง SoundPad ทั้งห้อง |
| `vote_cast` | `{ vote_id }` | โหวต yes สำหรับ vote ที่ active อยู่ |
| `voice_start` | — | PTT เริ่มพูด (WebRTC signaling) |
| `voice_stop` | — | PTT หยุดพูด |
| `voice_join` | `{ to: client_id }` | Listener แจ้ง Speaker ว่าพร้อมรับ offer |
| `voice_offer` | `{ to, sdp }` | Speaker ส่ง SDP offer |
| `voice_answer` | `{ to, sdp }` | Listener ส่ง SDP answer |
| `voice_ice` | `{ to, candidate }` | ICE candidate exchange |

> `song_id` ในทุก event หมายถึง `queue_id` (UUID) ไม่ใช่ YouTube Video ID

### Server → Client

| Event | Payload | คำอธิบาย |
|---|---|---|
| `room_joined` | room_id, queue, index, seek, is_playing, autoplay, shuffle, random_play, playback_speed, history, chat_history, online_users, soundpad | ส่งให้ Client หลัง join สำเร็จ |
| `queue_updated` | queue, index, is_playing, history | Broadcast เมื่อคิวเปลี่ยน |
| `seek_sync` | `{ seek_time, is_playing }` | Broadcast ทุก 5 วิ ขณะกำลังเล่น |
| `song_skipped` | `{ song_id, title, reason, error_code }` | Broadcast เมื่อข้ามเพลง |
| `playback_mode_updated` | `{ autoplay, shuffle, random_play, playback_speed }` | Broadcast เมื่อ mode เปลี่ยน |
| `user_joined` | `{ user, online_users }` | Broadcast เมื่อมีคนเข้าร่วม |
| `user_left` | `{ user, online_users }` | Broadcast เมื่อมีคน disconnect |
| `message_received` | `{ id, user, text, timestamp }` | Broadcast ข้อความแชท |
| `soundpad_updated` | `[SoundPadSlot \| null, ...]` (50 slots) | Broadcast เมื่อ pad เปลี่ยน |
| `soundpad_play` | `{ slot, video_id, triggered_by_client_id }` | Broadcast เมื่อมีคนกด play |
| `soundpad_stop` | — | Broadcast หยุดเสียง |
| `vote_started` | Vote object | Broadcast เมื่อเปิด vote ใหม่ |
| `vote_updated` | Vote object | Broadcast เมื่อมี yes vote เพิ่ม |
| `vote_resolved` | `{ vote, result }` | Broadcast เมื่อ vote สรุปผล |
| `voice_start` | `{ user_id, username, profile_img }` | Broadcast เมื่อมีคนเริ่ม PTT |
| `voice_stop` | `{ user_id }` | Broadcast เมื่อ PTT หยุด |
| `voice_join` | `{ from }` | Relay listener → speaker |
| `voice_offer` | `{ from, sdp }` | Relay speaker → listener |
| `voice_answer` | `{ from, sdp }` | Relay listener → speaker |
| `voice_ice` | `{ from, candidate }` | Relay ICE candidate |
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
| `INVALID_ROOM_ID` | room_id ไม่ใช่ตัวเลข 6 หลัก |
| `EMPTY_MESSAGE` | text ว่าง |
| `RATE_LIMITED` | ส่งเกิน rate limit |
| `DUPLICATE_SONG` | เพลงซ้ำใน queue |
| `QUEUE_FULL` | queue เต็ม (max 100) |
| `INVALID_URL` | YouTube URL ไม่ถูกต้อง |
| `SONG_NOT_FOUND` | ไม่พบ song_id ใน queue |
| `INVALID_PLAYBACK_MODE` | เปิด shuffle + random_play พร้อมกัน |
| `VOTE_IN_PROGRESS` | มีการโหวตอยู่แล้ว |
| `NO_ACTIVE_VOTE` | ไม่มี vote ที่ active |
| `ALREADY_VOTED` | โหวตแล้ว |
| `SERVER_ERROR` | ข้อผิดพลาดภายใน |

---

## Voting System

เมื่อผู้ใช้ต้องการ `remove_song` หรือ `skip_song` เพลงที่คนอื่น add ไว้:
- Server สร้าง Vote ใหม่ broadcast `vote_started` ไปทั้งห้อง
- ต้องการ `ceil(total_users / 2)` yes votes จึงผ่าน
- Vote หมดอายุใน **30 วินาที**
- ถ้าห้องมีคนเดียว — execute ทันที ไม่เปิด vote

---

## SoundPad

- 50 slots ต่อห้อง (index 0–49)
- แต่ละ slot เก็บ `video_id` + `title`
- `soundpad_play` → broadcast ไปทั้งห้อง ทุกคนเล่นพร้อมกัน โดยอิสระ (ไม่ queue ไม่รอ)
- ประวัติการเล่นบันทึกใน Redis (list)

---

## Voice PTT (WebRTC)

- ใช้ WebSocket เป็น signaling channel (ไม่มี media server)
- `voice_start` → broadcast แจ้งทั้งห้อง
- Listeners ส่ง `voice_join` → Speaker ส่ง `voice_offer` → Listener ตอบ `voice_answer` → แลก ICE candidates
- เป็น peer-to-peer: speaker connect กับทุก listener แยกกัน

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
  "duration": 0,
  "is_broadcast": false,
  "is_live": false
}
```

### SoundPadSlot
```json
{ "video_id": "YouTubeVideoID", "title": "ชื่อเสียง" }
```
slot ที่ว่างเปล่าเป็น `null`

### Vote
```json
{
  "id": "uuid",
  "action": "remove_song | skip_song",
  "song_queue_id": "uuid",
  "song_title": "ชื่อเพลง",
  "initiated_by": "Alice",
  "yes_voter_ids": ["user-id-1"],
  "total_at_start": 3,
  "expires_at": 1744299841000
}
```

---

## Daily Cleanup

ทุกวัน **06:00 Asia/Bangkok** backend จะล้าง Redis keys ทุกห้อง (`synctune:room:*`) โดยอัตโนมัติ

---

## โครงสร้างโปรเจ็กต์

```
synctune-backend/
├── main.go                    ← Entry point + daily cleanup goroutine
├── config/config.go           ← โหลด ENV Variables
├── model/
│   ├── playlist.go            ← Song, PlaylistState, SoundPad, BroadcastSchedule, TopSpender structs
│   ├── user.go                ← User, ChatMessage structs
│   ├── vote.go                ← Vote, VoteAction structs
│   └── errors.go              ← Sentinel errors
├── store/redis.go             ← Redis operations per-room (state/history/chat/soundpad/vote)
├── hub/hub.go                 ← WebSocket connection pool + multi-room routing
├── controller/
│   ├── queue.go               ← Queue + playback business logic
│   ├── chat.go                ← Chat + join/room logic
│   ├── soundpad.go            ← SoundPad handlers
│   ├── voice.go               ← Voice PTT WebRTC signaling handlers
│   └── vote.go                ← Voting system handlers
├── broadcaster/broadcaster.go ← Broadcast helper functions (per-room)
├── broadcast/scheduler.go     ← Scheduled broadcast (cron per room)
├── ticker/seekticker.go       ← seek_sync goroutine (per-room)
├── admin/admin.go             ← Admin panel + PromptPay QR + TopSpenders
├── promptpay/promptpay.go     ← PromptPay QR generation
└── youtube/metadata.go        ← oEmbed API + thumbnail fallback
```

---

## Changelog

### v1.0.1 (2026-05-16)
- SoundPad: 50 slots ต่อห้อง — set/clear/play/stop events พร้อมประวัติการเล่น
- Voice PTT: WebRTC signaling ผ่าน WebSocket (peer-to-peer, ไม่มี media server)
- Voting system: โหวต remove/skip เพลงที่คนอื่น add (TTL 30 วิ, majority vote)
- Playback Speed: field `playback_speed` ใน PlaylistState + `set_playback_mode`
- Broadcast Scheduler: cron-based scheduled broadcasts per room
- Top Spenders: CRUD ผ่าน Admin API + broadcast realtime
- Song fields เพิ่ม `is_broadcast` และ `is_live`
- ผู้ add เพลงสามารถ remove/skip เพลงตัวเองได้ทันทีโดยไม่ต้องโหวต

### v1.0.0 (2026-05-04)
- Initial release
- Multi-room WebSocket server ด้วย Melody
- Redis state persistence per room
- Queue, History, Chat แยกกันคนละห้อง
- Autoplay / Shuffle / Random playback logic
- seek_sync broadcast ทุก 5 วินาที
- Rate limiting per client
- Daily cleanup 06:00 Asia/Bangkok
- YouTube oEmbed metadata (title + thumbnail)
- Admin panel + PromptPay QR integration
- Docker + health/metrics endpoints

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
