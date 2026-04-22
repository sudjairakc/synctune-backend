package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/olahol/melody"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/synctune/backend/config"
	"github.com/synctune/backend/controller"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/store"
	"github.com/synctune/backend/ticker"
)

var bangkokLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		return time.FixedZone("ICT", 7*60*60)
	}
	return loc
}()

// corsMiddleware เพิ่ม CORS headers ทุก request
func corsMiddleware(allowedOrigins string, next http.Handler) http.Handler {
	origins := strings.Split(allowedOrigins, ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		allowed := ""
		for _, o := range origins {
			if o == "*" || o == origin {
				allowed = origin
				if o == "*" {
					allowed = "*"
				}
				break
			}
		}

		if allowed != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowed)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// startDailyCleanup รัน goroutine ที่ flush Redis ทุกวันเวลา 06:00 Asia/Bangkok
func startDailyCleanup(s store.Store, stopCh <-chan struct{}) {
	for {
		now := time.Now().In(bangkokLoc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, bangkokLoc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-time.After(time.Until(next)):
			if err := s.FlushAll(context.Background()); err != nil {
				log.Error().Err(err).Msg("daily cleanup: failed to flush redis")
			} else {
				log.Info().Str("time", next.Format(time.RFC3339)).Msg("daily cleanup: redis flushed")
			}
		case <-stopCh:
			return
		}
	}
}

func main() {
	cfg := config.Load()

	// Setup Logger
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Setup Redis
	redisStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Str("redis_url", cfg.RedisURL).Msg("failed to connect to redis")
	}
	log.Info().Str("redis_url", cfg.RedisURL).Msg("connected to redis")

	// Setup Hub
	h := hub.NewHub(redisStore)

	// ลงทะเบียน Message Handler
	h.SetMessageHandler(func(client *hub.Client, msg model.WSMessage) {
		switch msg.Event {
		case "add_song":
			controller.HandleAddSong(h, client, msg.Payload)
		case "remove_song":
			controller.HandleRemoveSong(h, client, msg.Payload)
		case "reorder_queue":
			controller.HandleReorderQueue(h, client, msg.Payload)
		case "report_error":
			controller.HandleReportError(h, client, msg.Payload)
		case "song_ended":
			controller.HandleSongEnded(h, client, msg.Payload)
		case "skip_song":
			controller.HandleSkipSong(h, client, msg.Payload)
		case "set_playback_mode":
			controller.HandleSetPlaybackMode(h, client, msg.Payload)
		case "set_playback_speed":
			controller.HandleSetPlaybackSpeed(h, client, msg.Payload)
		case "join":
			controller.HandleJoin(h, client, msg.Payload)
		case "send_message":
			controller.HandleSendMessage(h, client, msg.Payload)
		case "voice_start":
			controller.HandleVoiceStart(h, client, msg.Payload)
		case "voice_stop":
			controller.HandleVoiceStop(h, client, msg.Payload)
		case "voice_join":
			controller.HandleVoiceJoin(h, client, msg.Payload)
		case "voice_offer":
			controller.HandleVoiceOffer(h, client, msg.Payload)
		case "voice_answer":
			controller.HandleVoiceAnswer(h, client, msg.Payload)
		case "voice_ice":
			controller.HandleVoiceICE(h, client, msg.Payload)
		case "soundpad_set":
			controller.HandleSoundPadSet(h, client, msg.Payload)
		case "soundpad_clear":
			controller.HandleSoundPadClear(h, client, msg.Payload)
		case "soundpad_play":
			controller.HandleSoundPadPlay(h, client, msg.Payload)
		default:
			log.Warn().Str("event", msg.Event).Msg("unknown event received")
		}
	})

	go h.Run()

	// Setup Seek Ticker
	seekTicker := ticker.NewSeekTicker(
		time.Duration(cfg.SeekBroadcastInterval)*time.Second,
		h, redisStore,
	)
	seekTicker.Start()
	defer seekTicker.Stop()

	cleanupStop := make(chan struct{})
	go startDailyCleanup(redisStore, cleanupStop)
	defer close(cleanupStop)

	// Setup Melody (WebSocket)
	m := melody.New()
	m.Config.MaxMessageSize = 4096

	m.HandleConnect(func(s *melody.Session) {
		h.Register(s)
	})

	m.HandleDisconnect(func(s *melody.Session) {
		h.Unregister(s)
	})

	m.HandleMessage(func(s *melody.Session, msg []byte) {
		h.HandleMessage(s, msg)
	})

	// Setup Routes
	mux := http.NewServeMux()

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if err := m.HandleRequest(w, r); err != nil {
			log.Error().Err(err).Msg("websocket upgrade failed")
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active_connections": h.ClientCount(),
			"active_rooms":       h.RoomCount(),
		})
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: corsMiddleware(cfg.AllowedOrigins, mux),
	}

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-quit
		log.Info().Msg("shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Info().Str("port", cfg.Port).Msg("SyncTune backend starting")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server error")
	}
}
