# SKILL.md — synctune-backend
## คู่มือ Claude Code Skills สำหรับ Go Backend · v2.0.0

Product: **SyncTune** (no “2.0 Office” branding). Office map + LiveKit mint are features, not the product name.

ไฟล์นี้รวม skill patterns ที่ใช้บ่อยใน repo นี้ ให้ Claude Code ใช้เป็น cheatsheet ก่อนเริ่มงาน

---

## 1. เพิ่ม WebSocket Event ใหม่

### Pattern
1. เพิ่ม handler function ใน `controller/` package ที่เหมาะสม
2. Register handler ใน `hub/hub.go` switch-case ของ `handleMessage`
3. เพิ่ม broadcast helper ใน `broadcaster/broadcaster.go` ถ้าต้อง broadcast
4. อัปเดต CLAUDE.md section 5 + README.md WebSocket Events table

### Template handler
```go
func HandleXxx(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
    if !requireJoined(h, client) {
        return
    }
    var payload xxxPayload
    if err := json.Unmarshal(rawPayload, &payload); err != nil {
        return
    }
    // validate payload
    // load state from Redis
    // mutate state
    // save state to Redis
    // broadcast
}
```

---

## 2. เพิ่ม Redis Key ใหม่

### Pattern
1. เพิ่ม method ใน `store/redis.go`
2. ใช้ key pattern: `synctune:room:{roomID}:xxx`
3. ถ้า key ต้องถูก delete ตอนห้องว่าง → เพิ่มใน `store.DeleteRoom`
4. ถ้า key ต้องถูก cleanup รายวัน → `startDailyCleanup` ใน main.go จัดการอยู่แล้วผ่าน `synctune:room:*` pattern

```go
func (s *RedisStore) GetXxx(ctx context.Context, roomID string) (*model.Xxx, error) {
    key := fmt.Sprintf("synctune:room:%s:xxx", roomID)
    data, err := s.client.Get(ctx, key).Bytes()
    if errors.Is(err, redis.Nil) {
        return nil, nil
    }
    // unmarshal
}
```

---

## 3. เพิ่ม Rate Limit

```go
// ใน hub/hub.go หรือ controller
limiter := rate.NewLimiter(rate.Every(time.Minute/N), N)
if !limiter.Allow() {
    h.SendToSession(client.Conn, "error", model.WSError{Code: "RATE_LIMITED", ...})
    return
}
```

Rate limiters เก็บอยู่ใน `Client` struct (per-client, per-action)

---

## 4. เพิ่ม Broadcast Function

```go
// broadcaster/broadcaster.go
func BroadcastXxx(h *hub.Hub, roomID string, data SomeType) {
    h.BroadcastToRoom(roomID, "event_name", data)
}
```

ทุก broadcast function รับ roomID เสมอ — ไม่มี global broadcast

---

## 5. เขียน Test

```bash
# Unit test
go test ./controller/... -v -run TestHandleXxx

# Integration test (ต้องการ Docker)
go test ./... -tags=integration -v
```

Pattern สำหรับ controller test:
- ใช้ `hub.NewTestHub()` (ถ้ามี) หรือ mock hub
- Assert ผ่าน broadcast messages ที่ส่งออกมา

---

## 6. Office voice (LiveKit)

Product name: **SyncTune** (UI/docs — no “2.0 Office” branding).

- Mint JWT in `controller/livekit_token.go`; send `voice_credentials` on zone/bubble changes
- Invariant: `bubble > meeting > null`
- Needs `LIVEKIT_URL` + `LIVEKIT_API_KEY` + `LIVEKIT_API_SECRET`
- Map: `office/map_v2.json` — walk allow-list `floor|chair|door`
- Legacy mesh PTT stays in `controller/voice.go` for opt-in FE clients

---

## 7. Debug Tips

```bash
# ดู event ที่ส่งผ่าน WebSocket ทั้งหมด
LOG_LEVEL=debug go run main.go

# ตรวจ Redis state ของห้อง
redis-cli GET "synctune:room:123456:state" | jq .

# ดู soundpad
redis-cli GET "synctune:room:123456:soundpad" | jq .

# ดู vote ที่ active
redis-cli GET "synctune:room:123456:vote" | jq .

# ดู keys ทั้งหมดในห้อง
redis-cli KEYS "synctune:room:123456:*"
```

---

## 8. Common Pitfalls

| ปัญหา | วิธีแก้ |
|---|---|
| Race condition ใน hub.rooms | ใช้ hub.mu (sync.RWMutex) ทุกครั้ง |
| Goroutine leak | ทุก goroutine ต้องมี stopCh + select |
| ใช้ in-memory state แทน Redis | reload จาก Redis ก่อน handler ทุกตัว |
| Broadcast ไปนอกห้อง | ใช้ `h.BroadcastToRoom(roomID, ...)` ไม่ใช่ `h.Broadcast` |
| song_id vs queue_id | ทุก event ใช้ queue_id เสมอ |

---

## 9. Checklist ก่อน PR

- [ ] Handler ตรวจ `requireJoined` ก่อน
- [ ] Validate payload — return เงียบๆ ถ้า invalid (ไม่ crash)
- [ ] Reload state จาก Redis ก่อนตรวจ queue_id (deduplication)
- [ ] Goroutine ใหม่มี stop mechanism
- [ ] Key ใหม่ถูก delete ใน `DeleteRoom`
- [ ] อัปเดต CLAUDE.md + README.md
