// Package broadcast จัดการ scheduled broadcast — เล่นพักคิวของทุกห้องตามเวลาที่กำหนด
package broadcast

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
	"github.com/synctune/backend/youtube"
)

var videoIDRegex = regexp.MustCompile(`^[\w-]{11}$`)

// Scheduler จัดการ dynamic broadcast schedules
type Scheduler struct {
	mu     sync.Mutex
	cron   *cron.Cron
	h      *hub.Hub
	s      store.Store
	stopCh <-chan struct{}
	loc    *time.Location
}

// Start เริ่ม broadcast scheduler — โหลด schedules จาก Redis แล้วสร้าง cron
func Start(h *hub.Hub, s store.Store, stopCh <-chan struct{}) *Scheduler {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		log.Error().Err(err).Msg("broadcast: failed to load timezone, skipping")
		return nil
	}

	sc := &Scheduler{h: h, s: s, stopCh: stopCh, loc: loc}

	// migrate hardcoded schedules → Redis ถ้า Redis ยังว่างอยู่
	sc.migrateDefaults()

	sc.reload()

	go func() {
		<-stopCh
		sc.mu.Lock()
		if sc.cron != nil {
			sc.cron.Stop()
		}
		sc.mu.Unlock()
		log.Info().Msg("broadcast: scheduler stopped")
	}()

	return sc
}

// Reload หยุด cron เดิมแล้วสร้างใหม่จาก schedules ใน Redis
func (sc *Scheduler) Reload() {
	sc.reload()
}

// TriggerNow inject broadcast เข้าทุก active room ทันที (admin skip ad)
func (sc *Scheduler) TriggerNow(youtubeURL string) error {
	triggerBroadcast(sc.h, sc.s, youtubeURL)
	return nil
}

// SkipCurrentBroadcast หยุด broadcast ที่กำลังเล่นอยู่ในทุกห้องและคืน saved state
func (sc *Scheduler) SkipCurrentBroadcast() {
	ctx := context.Background()
	for _, roomID := range sc.h.ActiveRooms() {
		state, err := sc.s.GetState(ctx, roomID)
		if err != nil || !state.IsBroadcasting {
			continue
		}
		restoreSavedState(sc.h, sc.s, roomID, state)
	}
}

func (sc *Scheduler) reload() {
	ctx := context.Background()
	schedules, err := sc.s.GetSchedules(ctx)
	if err != nil {
		log.Error().Err(err).Msg("broadcast: failed to load schedules from redis")
		return
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.cron != nil {
		sc.cron.Stop()
	}

	enabled := 0
	sc.cron = cron.New(cron.WithLocation(sc.loc))
	for _, sched := range schedules {
		if !sched.Enabled {
			continue
		}
		sched := sched
		if _, err := sc.cron.AddFunc(sched.CronExpr, func() {
			triggerBroadcast(sc.h, sc.s, sched.YoutubeURL)
		}); err != nil {
			log.Error().Err(err).Str("cron", sched.CronExpr).Str("id", sched.ID).Msg("broadcast: invalid cron expression")
			continue
		}
		enabled++
	}
	sc.cron.Start()
	log.Info().Int("total", len(schedules)).Int("enabled", enabled).Msg("broadcast: scheduler reloaded")
}

func (sc *Scheduler) migrateDefaults() {
	ctx := context.Background()
	existing, err := sc.s.GetSchedules(ctx)
	if err != nil || len(existing) > 0 {
		return
	}

	defaults := []struct {
		cron  string
		url   string
		label string
	}{
		{"58 07 * * *", "https://www.youtube.com/watch?v=kvfblcLVxlo", "เพลงชาติไทย (เช้า)"},
		{"58 08 * * *", "https://www.youtube.com/watch?v=nrVrZau7M1M", "กสิกร"},
		{"58 09 * * *", "https://www.youtube.com/watch?v=j_k-aTGiwAI", "สีคอลลีน"},
		{"58 10 * * *", "https://www.youtube.com/watch?v=QWKn1dJv8Cg", "น้ำทิพย์"},
		{"58 11 * * *", "https://www.youtube.com/watch?v=8OwzJoECXJw", "ซาร่า"},
		{"30 12 * * *", "https://www.youtube.com/watch?v=Cfq1-Ryt4_s", "กินข้าว"},
		{"58 12 * * *", "https://www.youtube.com/watch?v=YH7bC7_f_1s", "ไลโอ"},
		{"58 13 * * *", "https://www.youtube.com/watch?v=bTNhluU-bdk", "ข้าวแสนดี"},
		{"58 14 * * *", "https://www.youtube.com/watch?v=atEEn01TyUs", "protriva"},
		{"58 15 * * *", "https://www.youtube.com/watch?v=-tjtAC69Wa8", "คานิว่า"},
		{"58 16 * * *", "https://www.youtube.com/watch?v=Ww3wlFzYFx0", "บสย."},
		{"58 17 * * *", "https://www.youtube.com/watch?v=8VXZKELPo88", "เพลงชาติไทย (เย็น)"},
	}

	schedules := make([]model.BroadcastSchedule, len(defaults))
	for i, d := range defaults {
		schedules[i] = model.BroadcastSchedule{
			ID:         uuid.New().String(),
			CronExpr:   d.cron,
			YoutubeURL: d.url,
			Label:      d.label,
			Enabled:    true,
		}
	}
	if err := sc.s.SetSchedules(ctx, schedules); err != nil {
		log.Error().Err(err).Msg("broadcast: failed to migrate default schedules")
	} else {
		log.Info().Int("count", len(schedules)).Msg("broadcast: migrated default schedules to redis")
	}
}

// triggerBroadcast inject broadcast ไปทุก active room
func triggerBroadcast(h *hub.Hub, s store.Store, youtubeURL string) {
	videoID, err := extractVideoID(youtubeURL)
	if err != nil {
		log.Error().Err(err).Str("url", youtubeURL).Msg("broadcast: invalid youtube url")
		return
	}

	meta, err := youtube.FetchMetadata(videoID)
	if err != nil {
		log.Warn().Err(err).Str("video_id", videoID).Msg("broadcast: failed to fetch metadata, using fallback")
		meta = &youtube.VideoMetadata{
			Title:     videoID,
			Thumbnail: "https://i.ytimg.com/vi/" + videoID + "/hqdefault.jpg",
		}
	}

	song := model.Song{
		QueueID:     uuid.New().String(),
		ID:          videoID,
		Title:       meta.Title,
		Thumbnail:   meta.Thumbnail,
		AddedBy:     "Broadcast",
		IsBroadcast: true,
	}

	rooms := h.ActiveRooms()
	if len(rooms) == 0 {
		log.Info().Str("video_id", videoID).Msg("broadcast: no active rooms, skipped")
		return
	}

	log.Info().Str("video_id", videoID).Str("title", song.Title).Int("rooms", len(rooms)).Msg("broadcast: triggering")
	for _, roomID := range rooms {
		triggerInRoom(h, s, roomID, song)
	}
}

// triggerInRoom inject broadcast เข้าห้องเดียว
func triggerInRoom(h *hub.Hub, s store.Store, roomID string, song model.Song) {
	ctx := context.Background()
	state, err := s.GetState(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("broadcast: failed to get state")
		return
	}

	if state.IsBroadcasting {
		state.BroadcastQueue = append(state.BroadcastQueue, song)
		if err := s.SetState(ctx, roomID, state); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Msg("broadcast: failed to queue pending broadcast")
		}
		log.Info().Str("room_id", roomID).Str("video_id", song.ID).Int("queued", len(state.BroadcastQueue)).Msg("broadcast: queued (room already broadcasting)")
		return
	}

	state.SavedQueue = state.CurrentQueue
	state.SavedCurrentIndex = state.CurrentIndex
	state.SavedSeekTime = state.SeekTime
	state.SavedIsPlaying = state.IsPlaying

	state.IsBroadcasting = true
	state.CurrentQueue = []model.Song{song}
	state.CurrentIndex = 0
	state.SeekTime = 0
	state.IsPlaying = true

	if err := s.SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("broadcast: failed to set state")
		return
	}

	log.Info().Str("room_id", roomID).Str("video_id", song.ID).Msg("broadcast: started in room")

	history, _ := s.GetHistory(ctx, roomID)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, history)
}

// restoreSavedState คืน state ก่อน broadcast สำหรับ admin skip
func restoreSavedState(h *hub.Hub, s store.Store, roomID string, state *model.PlaylistState) {
	ctx := context.Background()
	state.CurrentQueue = state.SavedQueue
	state.CurrentIndex = state.SavedCurrentIndex
	state.SeekTime = state.SavedSeekTime
	state.IsPlaying = state.SavedIsPlaying
	state.IsBroadcasting = false
	state.BroadcastQueue = nil
	state.SavedQueue = nil

	if err := s.SetState(ctx, roomID, state); err != nil {
		log.Error().Err(err).Str("room_id", roomID).Msg("broadcast: skip failed to restore state")
		return
	}
	log.Info().Str("room_id", roomID).Msg("broadcast: admin skipped broadcast, state restored")
	history, _ := s.GetHistory(ctx, roomID)
	broadcaster.BroadcastQueueUpdated(h, roomID, state, history)
}

// --- helpers ---

func extractVideoID(rawURL string) (string, error) {
	if rawURL == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	var videoID string
	switch u.Host {
	case "www.youtube.com", "youtube.com", "m.youtube.com", "music.youtube.com":
		if id, ok := strings.CutPrefix(u.Path, "/shorts/"); ok {
			videoID = id
		} else {
			videoID = u.Query().Get("v")
		}
	case "youtu.be":
		videoID = strings.TrimPrefix(u.Path, "/")
	default:
		return "", errors.New("not a youtube url")
	}

	if !videoIDRegex.MatchString(videoID) {
		return "", errors.New("invalid video id")
	}
	return videoID, nil
}
