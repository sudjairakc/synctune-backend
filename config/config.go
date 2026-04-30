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
		AdminToken:            getEnv("ADMIN_TOKEN", ""),
		PromptPayPhone:        getEnv("PROMPTPAY_PHONE", "0853997206"),
	}
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
