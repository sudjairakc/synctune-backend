package config

import "testing"

func TestLoadPresenceDefaultsNonZero(t *testing.T) {
	t.Setenv("PRESENCE_MAX_SPEED_PX_PER_SEC", "")
	t.Setenv("PRESENCE_MIN_INTERVAL_MS", "")
	t.Setenv("BELL_COOLDOWN_MS", "")
	t.Setenv("FOLLOW_STOP_DISTANCE_PX", "")

	cfg := Load()

	tests := []struct {
		name  string
		value int64
	}{
		{"PresenceMaxSpeedPxPerSec", int64(cfg.PresenceMaxSpeedPxPerSec)},
		{"PresenceMinIntervalMs", cfg.PresenceMinIntervalMs},
		{"BellCooldownMs", cfg.BellCooldownMs},
		{"FollowStopDistancePx", int64(cfg.FollowStopDistancePx)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value <= 0 {
				t.Fatalf("%s = %d, want > 0", tt.name, tt.value)
			}
		})
	}
}
