# DESIGN.md — synctune-backend
## Architecture Decisions & Design Notes · v2.0.0

ไฟล์นี้บันทึก design decisions ที่สำคัญ เหตุผลเบื้องหลัง และ trade-offs ที่เลือก

---

## 1. ทำไมถึงใช้ net/http + Melody แทน Fiber

**Decision:** ใช้ `net/http` standard library + Melody WebSocket library

**Reason:** Fiber ใช้ fasthttp ซึ่งไม่รองรับ `http.Hijacker` interface ที่ Melody ต้องการสำหรับ WebSocket upgrades ถ้าใช้ Fiber ต้องเปลี่ยน WebSocket library ทั้งหมด

**Trade-off:** net/http ช้ากว่า Fiber สำหรับ REST แต่ project นี้ traffic หลักคือ WebSocket ไม่ใช่ HTTP

---

## 2. Redis เป็น Single Source of Truth

**Decision:** ทุก state (queue, history, chat, soundpad, vote) เก็บใน Redis เท่านั้น — ไม่มี in-memory state ระดับ room

**Reason:**
- รองรับ horizontal scaling ในอนาคต
- หลีกเลี่ยง race condition จาก concurrent handlers
- State survive ถ้า backend restart (ถ้าไม่ trigger daily cleanup)

**Trade-off:** ทุก handler ต้อง round-trip Redis — latency สูงกว่า in-memory แต่ acceptable สำหรับ event-based system

**Critical:** Handler ต้อง reload state จาก Redis ก่อน check queue_id เสมอ (deduplication guard)

---

## 3. Multi-Room Architecture

**Decision:** Room ID = ตัวเลข 6 หลัก, สุ่มด้วย `crypto/rand`

**Reason:**
- ง่ายพิมพ์และจำ
- 900,000 combinations เพียงพอสำหรับ scale ปัจจุบัน
- crypto/rand หลีกเลี่ยง predictable room IDs

**Hub structure:**
```
hub.rooms = map[roomID]map[clientID]*Client
```
Broadcast เฉพาะ clients ในห้องเดียวกัน ไม่มี cross-room leakage

---

## 4. SoundPad — Independent Per-Client Playback

**Decision:** `soundpad_play` event ส่ง broadcast ไปทุก client แต่แต่ละ client จัดการ audio ของตัวเองอิสระ — ไม่มี global "soundpad is playing" state

**Reason:**
- ถ้าใช้ global state จะต้องจัดการ seek sync สำหรับ soundpad ด้วย (ซับซ้อนมาก)
- SoundPad ออกแบบมาเพื่อ "trigger" ไม่ใช่ "sync playback"
- Latency แตกต่างกันระหว่าง clients ทำให้ perfect sync เป็นไปไม่ได้อยู่แล้ว

**Trade-off:** Clients ที่ join ช้าจะไม่ได้ยิน sound ที่กำลังเล่นอยู่ — acceptable เพราะ soundpad เป็น ephemeral event

---

## 5. Office voice — LiveKit mint (primary) + legacy PTT relay

**Decision (current):** For meeting/bubble voice, backend **mints LiveKit JWTs only** (`voice_credentials`). No RoomService admin. Media stays in LiveKit. Invariant: `bubble > meeting > null`.

**Reason:** SFU fits multi-party office zones better than mesh; hub stays music/chat authority.

**Legacy:** WebRTC PTT signaling relay (`voice_start` / offer / answer / ice) remains for opt-in mesh clients.

**Trade-off:** Needs `LIVEKIT_URL` + API key/secret; without them credentials are empty and FE shows unavailable.

---

## 6. Voting System — Majority + TTL

**Decision:** Vote ต้องการ `ceil(total/2)` yes votes และ expire ใน 30 วินาที

**Reason:**
- Simple majority เข้าใจง่าย
- TTL ป้องกัน vote ค้างอยู่ถ้ามีคน disconnect ระหว่าง vote
- ถ้าห้องมีคนเดียว → execute ทันทีโดยไม่เปิด vote (UX ดีกว่า)

**Trade-off:** ถ้ามีคนออกจากห้องระหว่าง vote, `total_at_start` ไม่เปลี่ยน — คนที่เหลืออาจต้อง vote มากกว่าสัดส่วนจริง แต่ง่ายกว่าการ recalculate dynamic quorum

**Adder bypass:** ผู้ add เพลงสามารถ remove/skip เพลงตัวเองได้ทันที — สมเหตุสมผลเพราะเจ้าของ content

---

## 7. Daily Cleanup vs Room Auto-Delete

**Two mechanisms:**

| Mechanism | เมื่อไหร่ | ทำอะไร |
|---|---|---|
| `store.DeleteRoom` | Client คนสุดท้าย disconnect | ลบ keys ของห้องนั้นทันที |
| `startDailyCleanup` | 06:00 Asia/Bangkok ทุกวัน | SCAN+DEL ทุก `synctune:room:*` |

**Reason for both:** DeleteRoom ทำให้ห้องสะอาดทันที ป้องกัน stale data ถ้ามี bug ที่ทำให้ห้องไม่ถูกลบ daily cleanup เป็น safety net

---

## 8. Broadcast Scheduler

**Decision:** ใช้ `robfig/cron` สำหรับ scheduled broadcasts per room

**Reason:** Cron expression ยืดหยุ่น ใช้งานง่าย และ library นี้ stable

**Schedule storage:** เก็บใน Redis key `synctune:schedules` เป็น global list (ไม่ per-room) เพราะ admin จัดการ schedule จาก single admin panel

---

## 9. Rate Limiting Strategy

**Decision:** Per-client, per-action rate limiting ใช้ `golang.org/x/time/rate`

| Action | Limit | เหตุผล |
|---|---|---|
| `add_song` | 10/min | ป้องกัน queue spam |
| `report_error` | 5/min | ป้องกัน fake error reports |
| `send_message` | 30/min | ป้องกัน chat spam |

Rate limiters เก็บใน `Client` struct — ถูก GC เมื่อ client disconnect

---

## 10. Migration Guard

**Decision:** Songs เก่าที่ไม่มี `queue_id` field จะถูก backfill เป็น `{videoID}_{index}` ใน `store.GetState`

**Reason:** ป้องกัน panic/broken state เมื่อ deploy v1.0.0 ขึ้น prod ที่มี old data อยู่แล้ว

**When to remove:** เมื่อมั่นใจว่า Redis ทุก key ถูก migrate แล้ว (หลัง daily cleanup รอบแรกหลัง deploy)
