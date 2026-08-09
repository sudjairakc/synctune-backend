// Package controller — LiveKit token minting (join-via-token only; no RoomService).
package controller

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/config"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

const liveKitTokenTTL = 10 * time.Minute

// ErrLiveKitNotConfigured is returned when API key/secret are empty.
var ErrLiveKitNotConfigured = errors.New("livekit not configured")

// VoiceCredentials is the voice_credentials WebSocket payload.
// Empty GroupID clears the client voice session.
type VoiceCredentials struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	GroupID string `json:"group_id"`
}

// MintLiveKitToken signs a short-lived join token for a logical group.
// Room name = groupID; identity = connectionID. Does not call RoomService.
func MintLiveKitToken(apiKey, apiSecret, groupID, identity string, ttl time.Duration) (string, error) {
	if apiKey == "" || apiSecret == "" {
		return "", ErrLiveKitNotConfigured
	}
	if groupID == "" || identity == "" {
		return "", errors.New("group_id and identity are required")
	}
	if ttl <= 0 {
		ttl = liveKitTokenTTL
	}

	at := auth.NewAccessToken(apiKey, apiSecret)
	grant := &auth.VideoGrant{
		RoomJoin: true,
		Room:     groupID,
	}
	at.SetVideoGrant(grant).
		SetIdentity(identity).
		SetValidFor(ttl)

	return at.ToJWT()
}

// BuildVoiceCredentials mints credentials for groupID, or returns empty clear
// credentials when groupID is empty. Returns ErrLiveKitNotConfigured when keys are unset.
func BuildVoiceCredentials(cfg *config.Config, groupID, connectionID string) (VoiceCredentials, error) {
	if groupID == "" {
		return VoiceCredentials{}, nil
	}
	if cfg == nil || !cfg.LiveKitConfigured() {
		return VoiceCredentials{}, ErrLiveKitNotConfigured
	}

	token, err := MintLiveKitToken(cfg.LiveKitAPIKey, cfg.LiveKitAPISecret, groupID, connectionID, liveKitTokenTTL)
	if err != nil {
		return VoiceCredentials{}, err
	}
	return VoiceCredentials{
		URL:     cfg.LiveKitURL,
		Token:   token,
		GroupID: groupID,
	}, nil
}

// EmitVoiceCredentials sends voice_credentials to the client and returns the
// group_id that was actually emitted (empty when clearing or mint fails).
func EmitVoiceCredentials(h *hub.Hub, client *hub.Client, cfg *config.Config, groupID string) string {
	creds, err := BuildVoiceCredentials(cfg, groupID, client.ID)
	if err != nil {
		log.Warn().Err(err).Str("connection_id", client.ID).Str("group_id", groupID).
			Msg("EmitVoiceCredentials: mint failed; clearing voice")
		h.SendToSession(client.Conn, "voice_credentials", VoiceCredentials{})
		return ""
	}
	h.SendToSession(client.Conn, "voice_credentials", creds)
	return creds.GroupID
}

// SyncActiveVoice recomputes DeriveActiveVoiceGroup and emits credentials only when
// the desired group differs from the last successfully emitted group.
// ActiveVoiceGroup tracks credentials actually sent (empty after clear/mint failure)
// so a later sync can remint when LiveKit becomes available.
// Switching groups emits a single payload with the new group_id
// (client must dispose the previous LiveKit session before joining — never two groups).
func SyncActiveVoice(h *hub.Hub, client *hub.Client) {
	if h == nil || client == nil || client.RoomID == "" {
		return
	}
	m := office.DefaultMap()
	zoneID, zoneType := m.ZoneAt(client.LastX, client.LastY)
	groupID := office.DeriveActiveVoiceGroup(client.RoomID, client.BubbleID, zoneID, zoneType)
	if groupID == client.ActiveVoiceGroup {
		return
	}
	client.ActiveVoiceGroup = EmitVoiceCredentials(h, client, config.Load(), groupID)
}

// ClearVoiceOnDisconnect emits clear credentials when the connection had an active group.
func ClearVoiceOnDisconnect(h *hub.Hub, client *hub.Client) {
	if h == nil || client == nil || client.ActiveVoiceGroup == "" {
		return
	}
	client.ActiveVoiceGroup = EmitVoiceCredentials(h, client, config.Load(), "")
}

// HandleVoiceTokenRequest remints credentials for the client's current active group.
func HandleVoiceTokenRequest(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" || client.RoomID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}

	m := office.DefaultMap()
	zoneID, zoneType := m.ZoneAt(client.LastX, client.LastY)
	groupID := office.DeriveActiveVoiceGroup(client.RoomID, client.BubbleID, zoneID, zoneType)
	client.ActiveVoiceGroup = EmitVoiceCredentials(h, client, config.Load(), groupID)
}
