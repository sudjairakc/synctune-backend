# SyncTune Backend

> ⚠️ **Experimental project** — built for personal learning and exploration. Not intended for commercial use.

![Version](https://img.shields.io/badge/version-1.0.1-blue)

A Go backend for SyncTune — a real-time collaborative music listening app. Handles multi-room state, WebSocket broadcasting, SoundPad, Voice PTT signaling, and voting.

**Stack:** Go · net/http · Melody (WebSocket) · Redis · zerolog

---

## Getting Started

**Prerequisites:** Go 1.22+ and Docker (for Redis)

```bash
docker run -d --name synctune-redis -p 6379:6379 redis:alpine
cp .env.example .env
go mod tidy
go run main.go   # http://localhost:8080
```

---

## Commands

```bash
go run main.go                     # Run without hot reload
air                                # Hot reload (go install github.com/air-verse/air@latest)
LOG_LEVEL=debug go run main.go     # Verbose logging

go build -o synctune-backend .     # Build binary

go test ./... -v                   # Unit tests
go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out
go test ./... -tags=integration -v # Integration tests (requires Docker)

golangci-lint run                  # Lint
gofmt -w .                         # Format
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Port the backend listens on |
| `REDIS_URL` | `localhost:6379` | Redis connection URL (supports `redis://` format) |
| `SEEK_BROADCAST_INTERVAL` | `5` | Interval for seek_sync broadcasts (seconds) |
| `MAX_QUEUE_SIZE` | `100` | Maximum songs per queue |
| `RATE_LIMIT_ADD_SONG` | `10` | Max add_song events per minute per client |
| `LOG_LEVEL` | `info` | Log level: debug / info / warn / error |
| `ALLOWED_ORIGINS` | `*` | CORS allowed origins (comma-separated) |

---

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/ws` | WebSocket endpoint |
| `GET` | `/health` | Health check |
| `GET` | `/metrics` | Connection and room counts |
| `GET` | `/admin` | Admin panel UI |

```bash
curl http://localhost:8080/health
curl http://localhost:8080/metrics
```

---

## Project Structure

```
synctune-backend/
├── main.go                    ← Entry point + daily cleanup goroutine
├── config/config.go           ← Load ENV variables
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
├── broadcaster/broadcaster.go ← Broadcast helpers (per-room)
├── broadcast/scheduler.go     ← Cron-based scheduled broadcasts
├── ticker/seekticker.go       ← seek_sync goroutine (per-room)
├── admin/admin.go             ← Admin panel + PromptPay QR + Top Spenders API
├── promptpay/promptpay.go     ← PromptPay QR generation
└── youtube/metadata.go        ← oEmbed API + thumbnail fallback
```

---

## Multi-Room

- Each room has a **6-digit** numeric Room ID (e.g. `483921`)
- Send `join` without `room_id` → server creates a new room automatically
- Send `join` with `room_id` → join an existing room
- When the last client disconnects → room is deleted from Redis immediately
- Queue, History, Chat, and SoundPad are fully isolated per room

---

## WebSocket Events

### Client → Server

| Event | Payload | Description |
|---|---|---|
| `join` | `{ username, profile_img?, room_id? }` | Must be sent before any other event |
| `add_song` | `{ youtube_url, added_by }` | Add a song to the queue |
| `remove_song` | `{ song_id }` | Remove a song (may open a vote if added by someone else) |
| `reorder_queue` | `{ song_id, new_index }` | Reorder a song in the queue |
| `report_error` | `{ song_id, error_code }` | Report YouTube embed error 101 / 150 |
| `song_ended` | `{ song_id }` | Song finished — server advances the queue |
| `skip_song` | `{ song_id }` | Skip the current song (may open a vote if added by someone else) |
| `set_playback_mode` | `{ autoplay?, shuffle?, random_play?, playback_speed? }` | Update playback mode or speed |
| `send_message` | `{ text }` | Send a chat message |
| `soundpad_set` | `{ slot, video_id, title }` | Assign a video to a SoundPad slot (0–49) |
| `soundpad_clear` | `{ slot }` | Clear a SoundPad slot |
| `soundpad_play` | `{ slot }` | Trigger a slot — broadcast to all clients |
| `soundpad_stop` | — | Stop SoundPad audio — broadcast to all clients |
| `vote_cast` | `{ vote_id }` | Cast a yes vote for the active vote |
| `voice_start` | — | Start PTT (WebRTC signaling) |
| `voice_stop` | — | Stop PTT |
| `voice_join` | `{ to: client_id }` | Listener signals readiness to speaker |
| `voice_offer` | `{ to, sdp }` | Speaker sends SDP offer to listener |
| `voice_answer` | `{ to, sdp }` | Listener sends SDP answer to speaker |
| `voice_ice` | `{ to, candidate }` | ICE candidate exchange |

> `song_id` in all events refers to `queue_id` (UUID), not the YouTube video ID.

### Server → Client

| Event | Payload | Description |
|---|---|---|
| `room_joined` | room_id, queue, index, seek, is_playing, autoplay, shuffle, random_play, playback_speed, history, chat_history, online_users, soundpad | Sent to the joining client only |
| `queue_updated` | queue, index, is_playing, history | Broadcast when the queue changes |
| `seek_sync` | `{ seek_time, is_playing }` | Broadcast every 5 s while playing |
| `song_skipped` | `{ song_id, title, reason, error_code }` | Broadcast when a song is skipped |
| `playback_mode_updated` | `{ autoplay, shuffle, random_play, playback_speed }` | Broadcast when mode changes |
| `user_joined` | `{ user, online_users }` | Broadcast when someone joins |
| `user_left` | `{ user, online_users }` | Broadcast when someone disconnects |
| `message_received` | `{ id, user, text, timestamp }` | Broadcast chat message |
| `soundpad_updated` | `[SoundPadSlot \| null, ...]` (50 slots) | Broadcast when pad changes |
| `soundpad_play` | `{ slot, video_id, triggered_by_client_id }` | Broadcast play trigger |
| `soundpad_stop` | — | Broadcast stop trigger |
| `vote_started` | Vote object | Broadcast when a new vote opens |
| `vote_updated` | Vote object | Broadcast when a yes vote is added |
| `vote_resolved` | `{ vote, result }` | Broadcast when a vote concludes |
| `voice_start` | `{ user_id, username, profile_img }` | Broadcast when PTT starts |
| `voice_stop` | `{ user_id }` | Broadcast when PTT stops |
| `voice_join` | `{ from }` | Relay: listener → speaker |
| `voice_offer` | `{ from, sdp }` | Relay: speaker → listener |
| `voice_answer` | `{ from, sdp }` | Relay: listener → speaker |
| `voice_ice` | `{ from, candidate }` | Relay: ICE candidate |
| `error` | `{ code, message }` | Sent only to the client that caused the error |

#### song_skipped — reason values
| reason | Meaning |
|---|---|
| `user_skipped` | User pressed skip |
| `embed_not_allowed` | YouTube Error 101 |
| `embed_not_allowed_by_request` | YouTube Error 150 |

#### error — code values
| code | Cause |
|---|---|
| `NOT_JOINED` | Event sent before `join` |
| `INVALID_USERNAME` | Empty username |
| `INVALID_ROOM_ID` | room_id is not a 6-digit number |
| `EMPTY_MESSAGE` | Empty chat text |
| `RATE_LIMITED` | Exceeded rate limit |
| `DUPLICATE_SONG` | Song already in queue |
| `QUEUE_FULL` | Queue at max capacity (100) |
| `INVALID_URL` | Invalid YouTube URL |
| `SONG_NOT_FOUND` | song_id not found in queue |
| `INVALID_PLAYBACK_MODE` | shuffle and random_play enabled simultaneously |
| `VOTE_IN_PROGRESS` | A vote is already active |
| `NO_ACTIVE_VOTE` | No active vote found |
| `ALREADY_VOTED` | Client already voted |
| `SERVER_ERROR` | Internal server error |

---

## Features

- **Multi-room** — isolated queue, history, chat, and SoundPad per 6-digit room
- **Real-time sync** — seek_sync broadcast every 5 s while playing
- **Autoplay / Shuffle / Random** — server-side playback logic
- **Playback Speed** — synced via `set_playback_mode`
- **SoundPad** — 50 slots per room; play triggers broadcast to all clients independently
- **Voice PTT** — WebRTC signaling relay over WebSocket (peer-to-peer, no media server)
- **Voting** — majority vote for remove/skip on songs added by others; 30 s TTL
- **Rate limiting** — per-client, per-action limits
- **Daily cleanup** — Redis room keys purged at 06:00 Asia/Bangkok
- **Broadcast Scheduler** — cron-based scheduled song broadcasts per room
- **Top Spenders** — leaderboard via Admin API with real-time broadcast
- **Admin panel** — room management + PromptPay QR

---

## Important Notes

- **join first** — the server returns `NOT_JOINED` for any event received before `join`
- **queue_id vs video_id** — all events use `queue_id` (UUID), never the YouTube video ID
- **SoundPad is independent** — each client manages its own audio; no global playback state
- **Voice PTT is peer-to-peer** — backend only relays WebRTC signaling messages
- **Adder bypass** — the user who added a song can remove or skip it without a vote

---

## Data Objects

### Song
```json
{
  "queue_id": "uuid-per-slot",
  "id": "YouTubeVideoID",
  "title": "Song Title",
  "thumbnail": "https://i.ytimg.com/vi/.../maxresdefault.jpg",
  "added_by": "Alice",
  "duration": 213,
  "is_broadcast": false,
  "is_live": false
}
```

### SoundPadSlot
```json
{ "video_id": "YouTubeVideoID", "title": "Sound Name" }
```
Empty slots are `null`.

### Vote
```json
{
  "id": "uuid",
  "action": "remove_song | skip_song",
  "song_queue_id": "uuid",
  "song_title": "Song Title",
  "initiated_by": "Alice",
  "yes_voter_ids": ["user-id-1"],
  "total_at_start": 3,
  "expires_at": 1744299841000
}
```

---

## Docker

```bash
docker build -t synctune-backend .

docker run -d --name synctune-redis -p 6379:6379 redis:alpine
docker run -d \
  --name synctune-backend \
  -p 8080:8080 \
  -e REDIS_URL=synctune-redis:6379 \
  --link synctune-redis \
  synctune-backend
```

---

## Changelog

### v1.0.1 (2026-05-16)
- SoundPad: 50 slots per room — set/clear/play/stop with play history
- Voice PTT: WebRTC signaling relay over WebSocket (peer-to-peer, no media server)
- Voting: majority vote for remove/skip on songs added by others (30 s TTL)
- Playback Speed: `playback_speed` field in PlaylistState + `set_playback_mode`
- Broadcast Scheduler: cron-based scheduled broadcasts per room
- Top Spenders: CRUD via Admin API + real-time broadcast
- Song fields: added `is_broadcast` and `is_live`
- Adder bypass: song owner can remove/skip without a vote

### v1.0.0 (2026-05-04)
- Initial release
- Multi-room WebSocket server with Melody
- Redis state persistence per room
- Queue, History, Chat isolated per room
- Autoplay / Shuffle / Random playback logic
- seek_sync broadcast every 5 seconds
- Rate limiting per client
- Daily cleanup at 06:00 Asia/Bangkok
- YouTube oEmbed metadata (title + thumbnail)
- Admin panel + PromptPay QR integration
- Docker + health/metrics endpoints

---

## License

This project is released for personal and educational use only. Not licensed for commercial use.
