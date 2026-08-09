package config

import (
	"os"
	"strconv"
)

// Config เก็บค่า Configuration ทั้งหมดของระบบ
type Config struct {
	Port                  string
	RedisURL              string
	SeekBroadcastInterval int // วินาที
	MaxQueueSize          int
	RateLimitAddSong      int // ครั้ง/นาที
	LogLevel              string
	AllowedOrigins        string // comma-separated, "*" = allow all
	AdminToken            string // Bearer token สำหรับ /admin endpoints
	PromptPayPhone        string // เบอร์โทรสำหรับ PromptPay QR
	PresenceMaxSpeedPxPerSec int   // px/s — clamp ความเร็วเคลื่อนที่
	PresenceMinIntervalMs    int64 // ms — ความถี่ขั้นต่ำของ presence update (~15 Hz)
	BellCooldownMs           int64 // ms — cooldown ระหว่าง ring bell
	FollowStopDistancePx     int   // px — ระยะหยุด follow
	LiveKitURL               string
	LiveKitAPIKey            string
	LiveKitAPISecret         string
}

// Load อ่านค่าจาก Environment Variables พร้อม Default fallback
func Load() *Config {
	return &Config{
		Port:                  getEnv("PORT", "8080"),
		RedisURL:              getEnv("REDIS_URL", "localhost:6379"),
		SeekBroadcastInterval: getEnvInt("SEEK_BROADCAST_INTERVAL", 5),
		MaxQueueSize:          getEnvInt("MAX_QUEUE_SIZE", 100),
		RateLimitAddSong:      getEnvInt("RATE_LIMIT_ADD_SONG", 10),
		LogLevel:              getEnv("LOG_LEVEL", "info"),
		AllowedOrigins:        getEnv("ALLOWED_ORIGINS", "*"),
		AdminToken:               getEnv("ADMIN_TOKEN", ""),
		PromptPayPhone:           getEnv("PROMPTPAY_PHONE", "0853997206"),
		PresenceMaxSpeedPxPerSec: getEnvInt("PRESENCE_MAX_SPEED_PX_PER_SEC", 240),
		PresenceMinIntervalMs:    getEnvInt64("PRESENCE_MIN_INTERVAL_MS", 66),
		BellCooldownMs:           getEnvInt64("BELL_COOLDOWN_MS", 5000),
		FollowStopDistancePx:     getEnvInt("FOLLOW_STOP_DISTANCE_PX", 48),
		LiveKitURL:               getEnv("LIVEKIT_URL", ""),
		LiveKitAPIKey:            getEnv("LIVEKIT_API_KEY", ""),
		LiveKitAPISecret:         getEnv("LIVEKIT_API_SECRET", ""),
	}
}

// LiveKitConfigured reports whether API key + secret are set for token minting.
func (c *Config) LiveKitConfigured() bool {
	return c != nil && c.LiveKitAPIKey != "" && c.LiveKitAPISecret != ""
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}
