// Package broadcast จัดการ scheduled broadcast — เล่นพักคิวของทุกห้องตามเวลาที่กำหนด
package broadcast

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
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

// Schedule คือ 1 รายการ broadcast ที่ตั้งเวลาไว้
type Schedule struct {
	CronExpr   string // standard cron: "MIN HOUR * * *" (Asia/Bangkok)
	YoutubeURL string
}

// Schedules — hardcode broadcast schedules ที่นี่
// ตัวอย่าง: {CronExpr: "58 17 * * *", YoutubeURL: "https://youtu.be/dQw4w9WgXcQ"}
var Schedules = []Schedule{
	{CronExpr: "58 07 * * *", YoutubeURL: "https://www.youtube.com/watch?v=kvfblcLVxlo"}, //เพลงชาติไทย
	{CronExpr: "58 08 * * *", YoutubeURL: "https://www.youtube.com/watch?v=nrVrZau7M1M"}, // กสิกร
	{CronExpr: "58 09 * * *", YoutubeURL: "https://www.youtube.com/watch?v=j_k-aTGiwAI"}, // สีคอลลีน
	{CronExpr: "58 10 * * *", YoutubeURL: "https://www.youtube.com/watch?v=QWKn1dJv8Cg"}, // น้ำทิพย์
	{CronExpr: "58 11 * * *", YoutubeURL: "https://www.youtube.com/watch?v=8OwzJoECXJw"}, // ซาร่า
	{CronExpr: "30 12 * * *", YoutubeURL: "https://www.youtube.com/watch?v=Cfq1-Ryt4_s"}, // กินข้าว
	{CronExpr: "58 12 * * *", YoutubeURL: "https://www.youtube.com/watch?v=YH7bC7_f_1s"}, // ไลโอ
	{CronExpr: "58 13 * * *", YoutubeURL: "https://www.youtube.com/watch?v=bTNhluU-bdk"}, // ข้าวแสนดี
	{CronExpr: "58 14 * * *", YoutubeURL: "https://www.youtube.com/watch?v=atEEn01TyUs"}, // protriva
	{CronExpr: "58 15 * * *", YoutubeURL: "https://www.youtube.com/watch?v=-tjtAC69Wa8"}, // คานิว่า
	{CronExpr: "58 16 * * *", YoutubeURL: "https://www.youtube.com/watch?v=Ww3wlFzYFx0"}, // บสย.
	{CronExpr: "58 17 * * *", YoutubeURL: "https://www.youtube.com/watch?v=8VXZKELPo88"}, //เพลงชาติไทย
}

var videoIDRegex = regexp.MustCompile(`^[\w-]{11}$`)

// Start เริ่ม cron scheduler สำหรับ broadcast — เรียกครั้งเดียวตอน startup
func Start(h *hub.Hub, s store.Store, stopCh <-chan struct{}) {
	if len(Schedules) == 0 {
		log.Info().Msg("broadcast: no schedules configured, skipping")
		return
	}

	bangkokLoc, err := loadBangkokLoc()
	if err != nil {
		log.Error().Err(err).Msg("broadcast: failed to load timezone, skipping")
		return
	}

	c := cron.New(cron.WithLocation(bangkokLoc))
	for _, sched := range Schedules {
		sched := sched
		if _, err := c.AddFunc(sched.CronExpr, func() {
			triggerBroadcast(h, s, sched.YoutubeURL)
		}); err != nil {
			log.Error().Err(err).Str("cron", sched.CronExpr).Str("url", sched.YoutubeURL).Msg("broadcast: invalid cron expression")
		}
	}
	c.Start()

	go func() {
		<-stopCh
		c.Stop()
		log.Info().Msg("broadcast: scheduler stopped")
	}()

	log.Info().Int("schedules", len(Schedules)).Msg("broadcast: scheduler started")
}

// triggerBroadcast เรียกเมื่อ cron ยิง — inject broadcast ไปทุก active room
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
		// broadcast กำลังเล่นอยู่ — ต่อคิวไว้
		state.BroadcastQueue = append(state.BroadcastQueue, song)
		if err := s.SetState(ctx, roomID, state); err != nil {
			log.Error().Err(err).Str("room_id", roomID).Msg("broadcast: failed to queue pending broadcast")
		}
		log.Info().Str("room_id", roomID).Str("video_id", song.ID).Int("queued", len(state.BroadcastQueue)).Msg("broadcast: queued (room already broadcasting)")
		return
	}

	// บันทึก state ปัจจุบันของห้องไว้
	state.SavedQueue = state.CurrentQueue
	state.SavedCurrentIndex = state.CurrentIndex
	state.SavedSeekTime = state.SeekTime
	state.SavedIsPlaying = state.IsPlaying

	// inject broadcast
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

// --- helpers ---

func loadBangkokLoc() (*time.Location, error) {
	return time.LoadLocation("Asia/Bangkok")
}

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
