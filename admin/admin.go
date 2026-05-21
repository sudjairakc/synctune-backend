// Package admin จัดการ HTTP handlers สำหรับ admin panel
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
)

// Reloader คือ interface สำหรับ broadcast.Scheduler
type Reloader interface {
	Reload()
	TriggerNow(youtubeURL string) error
	SkipCurrentBroadcast()
}

// Handler รวม dependencies ของ admin
type Handler struct {
	h         *hub.Hub
	s         store.Store
	scheduler Reloader
	token     string
}

// New สร้าง admin Handler
func New(h *hub.Hub, s store.Store, scheduler Reloader, token string) *Handler {
	return &Handler{h: h, s: s, scheduler: scheduler, token: token}
}

// Register ลงทะเบียน routes กับ mux
func (a *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/admin/stats", a.auth(http.HandlerFunc(a.handleStats)))
	mux.Handle("/admin/rooms", a.auth(http.HandlerFunc(a.handleRooms)))
	mux.Handle("/admin/rooms/kick", a.auth(http.HandlerFunc(a.handleKick)))
	mux.Handle("/admin/schedules", a.auth(http.HandlerFunc(a.handleSchedules)))
	mux.Handle("/admin/schedules/trigger", a.auth(http.HandlerFunc(a.handleTrigger)))
	mux.Handle("/admin/broadcast/skip", a.auth(http.HandlerFunc(a.handleSkipBroadcast)))
	mux.Handle("/admin/top-spenders", a.auth(http.HandlerFunc(a.handleTopSpenders)))
	mux.Handle("/admin/settings", a.auth(http.HandlerFunc(a.handleSettings)))
	mux.HandleFunc("/top-spenders", a.handlePublicTopSpenders)
}

// auth middleware — ตรวจ Bearer token หรือ ?token= query param
func (a *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			jsonError(w, "admin token not configured", http.StatusServiceUnavailable)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != a.token {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GET /admin/stats — dashboard stats
func (a *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()
	rooms := a.h.RoomsDetail()

	totalUsers := 0
	totalSongs := 0
	totalSoundpads := 0

	for _, room := range rooms {
		totalUsers += len(room.Users)
		state, err := a.s.GetState(ctx, room.RoomID)
		if err == nil {
			totalSongs += len(state.CurrentQueue)
		}
		pad, err := a.s.GetSoundPad(ctx, room.RoomID)
		if err == nil {
			for _, slot := range pad {
				if slot != nil {
					totalSoundpads++
				}
			}
		}
	}

	jsonOK(w, map[string]interface{}{
		"rooms":      len(rooms),
		"users":      totalUsers,
		"songs":      totalSongs,
		"soundpads":  totalSoundpads,
	})
}

// GET /admin/rooms — รายละเอียดทุกห้อง
func (a *Handler) handleRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()
	rooms := a.h.RoomsDetail()

	type roomInfo struct {
		hub.RoomDetail
		Songs      int `json:"songs"`
		Soundpads  int `json:"soundpads"`
	}

	result := make([]roomInfo, 0, len(rooms))
	for _, room := range rooms {
		songs := 0
		soundpads := 0
		if state, err := a.s.GetState(ctx, room.RoomID); err == nil {
			songs = len(state.CurrentQueue)
		}
		if pad, err := a.s.GetSoundPad(ctx, room.RoomID); err == nil {
			for _, slot := range pad {
				if slot != nil {
					soundpads++
				}
			}
		}
		result = append(result, roomInfo{RoomDetail: room, Songs: songs, Soundpads: soundpads})
	}
	jsonOK(w, result)
}

// POST /admin/rooms/kick — เตะ user ออกด้วย client_id
func (a *Handler) handleKick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ClientID == "" {
		jsonError(w, "client_id required", http.StatusBadRequest)
		return
	}
	if !a.h.KickClient(body.ClientID) {
		jsonError(w, "client not found", http.StatusNotFound)
		return
	}
	log.Info().Str("client_id", body.ClientID).Msg("admin: kicked client")
	jsonOK(w, map[string]string{"status": "kicked"})
}

// GET/POST/PUT/DELETE /admin/schedules — CRUD schedules
func (a *Handler) handleSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	switch r.Method {
	case http.MethodGet:
		schedules, err := a.s.GetSchedules(ctx)
		if err != nil {
			jsonError(w, "failed to load schedules", http.StatusInternalServerError)
			return
		}
		jsonOK(w, schedules)

	case http.MethodPost:
		var body model.BroadcastSchedule
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		if body.CronExpr == "" || body.YoutubeURL == "" {
			jsonError(w, "cron_expr and youtube_url required", http.StatusBadRequest)
			return
		}
		body.ID = uuid.New().String()
		body.Enabled = true
		schedules, _ := a.s.GetSchedules(ctx)
		schedules = append(schedules, body)
		if err := a.s.SetSchedules(ctx, schedules); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.scheduler.Reload()
		jsonOK(w, body)

	case http.MethodPut:
		var body model.BroadcastSchedule
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			jsonError(w, "invalid body or missing id", http.StatusBadRequest)
			return
		}
		schedules, _ := a.s.GetSchedules(ctx)
		found := false
		for i, s := range schedules {
			if s.ID == body.ID {
				schedules[i] = body
				found = true
				break
			}
		}
		if !found {
			jsonError(w, "schedule not found", http.StatusNotFound)
			return
		}
		if err := a.s.SetSchedules(ctx, schedules); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.scheduler.Reload()
		jsonOK(w, body)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			jsonError(w, "id required", http.StatusBadRequest)
			return
		}
		schedules, _ := a.s.GetSchedules(ctx)
		filtered := schedules[:0]
		for _, s := range schedules {
			if s.ID != id {
				filtered = append(filtered, s)
			}
		}
		if err := a.s.SetSchedules(ctx, filtered); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.scheduler.Reload()
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /admin/schedules/trigger — play youtube URL ทันทีในทุกห้อง
func (a *Handler) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		YoutubeURL string `json:"youtube_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.YoutubeURL == "" {
		jsonError(w, "youtube_url required", http.StatusBadRequest)
		return
	}
	if err := a.scheduler.TriggerNow(body.YoutubeURL); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Info().Str("url", body.YoutubeURL).Msg("admin: triggered broadcast")
	jsonOK(w, map[string]string{"status": "triggered"})
}

// GET/POST /admin/settings — อ่านหรืออัปเดต global app settings
func (a *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	switch r.Method {
	case http.MethodGet:
		settings, err := a.s.GetSettings(ctx)
		if err != nil {
			jsonError(w, "failed to load settings", http.StatusInternalServerError)
			return
		}
		jsonOK(w, settings)

	case http.MethodPost:
		var body model.AppSettings
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := a.s.SetSettings(ctx, &body); err != nil {
			jsonError(w, "failed to save settings", http.StatusInternalServerError)
			return
		}
		a.h.BroadcastToAll("settings_updated", body)
		log.Info().Bool("allow_skip_broadcast", body.AllowSkipBroadcast).Msg("admin: settings updated")
		jsonOK(w, body)

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /admin/broadcast/skip — skip broadcast ที่กำลังเล่นในทุกห้อง
func (a *Handler) handleSkipBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.scheduler.SkipCurrentBroadcast()
	log.Info().Msg("admin: skipped current broadcast")
	jsonOK(w, map[string]string{"status": "skipped"})
}

// mergeTopSpenders รวมยอดของ spenders ที่ชื่อเดียวกัน (case-insensitive)
func mergeTopSpenders(spenders []model.TopSpender) []model.TopSpender {
	type entry struct {
		id     string
		name   string
		amount int
		date   string
	}
	order := []string{}
	m := map[string]*entry{}
	for _, sp := range spenders {
		key := strings.ToLower(strings.TrimSpace(sp.Name))
		if e, ok := m[key]; ok {
			e.amount += sp.Amount
			if sp.Date > e.date {
				e.date = sp.Date
			}
		} else {
			m[key] = &entry{id: sp.ID, name: sp.Name, amount: sp.Amount, date: sp.Date}
			order = append(order, key)
		}
	}
	result := make([]model.TopSpender, 0, len(order))
	for _, key := range order {
		e := m[key]
		result = append(result, model.TopSpender{ID: e.id, Name: e.name, Amount: e.amount, Date: e.date})
	}
	return result
}

// GET /top-spenders — public endpoint (ไม่ต้อง auth)
func (a *Handler) handlePublicTopSpenders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	spenders, err := a.s.GetTopSpenders(context.Background())
	if err != nil {
		jsonError(w, "failed to load top spenders", http.StatusInternalServerError)
		return
	}
	jsonOK(w, mergeTopSpenders(spenders))
}

// broadcastTopSpenders broadcast top_spenders_updated ไปทุกห้อง
// newSpender ใส่ข้อมูลคนที่เพิ่งโดเนท (nil = แค่ refresh ไม่ต้อง announce)
func (a *Handler) broadcastTopSpenders(ctx context.Context, newSpender *model.TopSpender) {
	spenders, err := a.s.GetTopSpenders(ctx)
	if err != nil {
		return
	}
	payload := map[string]interface{}{
		"spenders": mergeTopSpenders(spenders),
	}
	if newSpender != nil {
		payload["action"] = "donated"
		payload["new_spender"] = map[string]interface{}{
			"name":   newSpender.Name,
			"amount": newSpender.Amount,
		}
	}
	a.h.BroadcastToAll("top_spenders_updated", payload)
}

// GET/POST/PUT/DELETE /admin/top-spenders — CRUD top spenders
func (a *Handler) handleTopSpenders(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	switch r.Method {
	case http.MethodGet:
		spenders, err := a.s.GetTopSpenders(ctx)
		if err != nil {
			jsonError(w, "failed to load top spenders", http.StatusInternalServerError)
			return
		}
		jsonOK(w, spenders)

	case http.MethodPost:
		var body model.TopSpender
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid body", http.StatusBadRequest)
			return
		}
		if body.Name == "" || body.Amount <= 0 || body.Date == "" {
			jsonError(w, "name, amount and date required", http.StatusBadRequest)
			return
		}
		body.ID = uuid.New().String()
		spenders, _ := a.s.GetTopSpenders(ctx)
		spenders = append(spenders, body)
		if err := a.s.SetTopSpenders(ctx, spenders); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.broadcastTopSpenders(ctx, &body)
		jsonOK(w, body)

	case http.MethodPut:
		var body model.TopSpender
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			jsonError(w, "invalid body or missing id", http.StatusBadRequest)
			return
		}
		spenders, _ := a.s.GetTopSpenders(ctx)
		found := false
		for i, sp := range spenders {
			if sp.ID == body.ID {
				spenders[i] = body
				found = true
				break
			}
		}
		if !found {
			jsonError(w, "top spender not found", http.StatusNotFound)
			return
		}
		if err := a.s.SetTopSpenders(ctx, spenders); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.broadcastTopSpenders(ctx, nil)
		jsonOK(w, body)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			jsonError(w, "id required", http.StatusBadRequest)
			return
		}
		spenders, _ := a.s.GetTopSpenders(ctx)
		filtered := spenders[:0]
		for _, sp := range spenders {
			if sp.ID != id {
				filtered = append(filtered, sp)
			}
		}
		if err := a.s.SetTopSpenders(ctx, filtered); err != nil {
			jsonError(w, "failed to save", http.StatusInternalServerError)
			return
		}
		a.broadcastTopSpenders(ctx, nil)
		jsonOK(w, map[string]string{"status": "deleted"})

	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
