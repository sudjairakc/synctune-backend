package controller

import (
	"errors"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	"github.com/synctune/backend/config"
)

func TestMintLiveKitTokenClaims(t *testing.T) {
	const (
		apiKey    = "devkey"
		apiSecret = "secret"
		groupID   = "st:123456:meet:meeting-a"
		identity  = "conn-abc-1"
		ttl       = 10 * time.Minute
	)

	tokenStr, err := MintLiveKitToken(apiKey, apiSecret, groupID, identity, ttl)
	if err != nil {
		t.Fatalf("MintLiveKitToken: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := jwt.ParseSigned(tokenStr)
	if err != nil {
		t.Fatalf("ParseSigned: %v", err)
	}

	var claims jwt.Claims
	var grant auth.ClaimGrants
	if err := parsed.Claims([]byte(apiSecret), &claims, &grant); err != nil {
		t.Fatalf("Claims: %v", err)
	}

	// LiveKit puts identity in JWT subject (ClaimGrants.Identity is json:"-").
	if claims.Subject != identity {
		t.Fatalf("subject/identity = %q, want %q", claims.Subject, identity)
	}
	if grant.Video == nil || grant.Video.Room != groupID {
		t.Fatalf("video.room = %#v, want %q", grant.Video, groupID)
	}
	if !grant.Video.RoomJoin {
		t.Fatal("expected RoomJoin grant")
	}
	if claims.Expiry == nil || !claims.Expiry.Time().After(time.Now()) {
		t.Fatalf("expiry = %v, want future", claims.Expiry)
	}
	// short TTL: expire within ~15m (not the SDK default 6h)
	if claims.Expiry.Time().After(time.Now().Add(15 * time.Minute)) {
		t.Fatalf("expiry too far: %v", claims.Expiry.Time())
	}
}

func TestMintLiveKitTokenNotConfigured(t *testing.T) {
	_, err := MintLiveKitToken("", "secret", "room", "id", liveKitTokenTTL)
	if !errors.Is(err, ErrLiveKitNotConfigured) {
		t.Fatalf("err = %v, want ErrLiveKitNotConfigured", err)
	}
}

func TestBuildVoiceCredentials(t *testing.T) {
	cfg := &config.Config{
		LiveKitURL:       "wss://livekit.example",
		LiveKitAPIKey:    "devkey",
		LiveKitAPISecret: "secret",
	}

	t.Run("clear when empty group", func(t *testing.T) {
		creds, err := BuildVoiceCredentials(cfg, "", "conn-1")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if creds.GroupID != "" || creds.Token != "" {
			t.Fatalf("want empty clear credentials, got %+v", creds)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		_, err := BuildVoiceCredentials(&config.Config{}, "st:1:meet:a", "conn-1")
		if !errors.Is(err, ErrLiveKitNotConfigured) {
			t.Fatalf("err = %v, want ErrLiveKitNotConfigured", err)
		}
	})

	t.Run("mint ok", func(t *testing.T) {
		creds, err := BuildVoiceCredentials(cfg, "st:1:bubble:b1", "conn-1")
		if err != nil {
			t.Fatalf("BuildVoiceCredentials: %v", err)
		}
		if creds.URL != cfg.LiveKitURL || creds.GroupID != "st:1:bubble:b1" || creds.Token == "" {
			t.Fatalf("unexpected creds: %+v", creds)
		}
	})
}

func TestLiveKitConfigured(t *testing.T) {
	if (&config.Config{}).LiveKitConfigured() {
		t.Fatal("empty config should not be configured")
	}
	if !(&config.Config{LiveKitAPIKey: "k", LiveKitAPISecret: "s"}).LiveKitConfigured() {
		t.Fatal("key+secret should be configured")
	}
}
