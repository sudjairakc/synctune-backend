package controller

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
	"github.com/synctune/backend/office"
)

const joinChannelHistoryLimit = 50

// loadJoinChannelHistories loads meeting / bubble / DM histories the joiner may see.
func loadJoinChannelHistories(h *hub.Hub, client *hub.Client, spawned model.Presence) map[string][]model.ChatMessage {
	out := map[string][]model.ChatMessage{}
	if h == nil || client == nil || client.RoomID == "" {
		return out
	}
	ctx := context.Background()
	roomID := client.RoomID
	s := h.Store()

	m := office.DefaultMap()
	zoneID, zt := m.ZoneAt(spawned.X, spawned.Y)
	if zt == office.ZoneMeeting && zoneID != "" {
		putChannelHistory(ctx, s, roomID, "meeting:"+zoneID, out)
	}

	bubbleID := spawned.BubbleID
	if bubbleID == "" {
		bubbleID = client.BubbleID
	}
	if bubbleID != "" {
		putChannelHistory(ctx, s, roomID, "bubble:"+bubbleID, out)
	}

	keys, err := s.ListChannelKeys(ctx, roomID, "dm:")
	if err != nil {
		log.Warn().Err(err).Str("room_id", roomID).Msg("loadJoinChannelHistories: ListChannelKeys failed")
		return out
	}
	uid := client.User.ID
	for _, ch := range keys {
		if dmChannelIncludesUser(ch, uid) {
			putChannelHistory(ctx, s, roomID, ch, out)
		}
	}
	return out
}

func putChannelHistory(ctx context.Context, s hubStoreChannelHistory, roomID, channel string, out map[string][]model.ChatMessage) {
	msgs, err := s.GetChannelHistory(ctx, roomID, channel, joinChannelHistoryLimit)
	if err != nil {
		log.Warn().Err(err).Str("room_id", roomID).Str("channel", channel).Msg("putChannelHistory failed")
		return
	}
	if len(msgs) == 0 {
		return
	}
	out[channel] = msgs
}

type hubStoreChannelHistory interface {
	GetChannelHistory(ctx context.Context, roomID, channel string, limit int) ([]model.ChatMessage, error)
}

// dmChannelIncludesUser — channel "dm:{idA}:{idB}" contains userID as either side.
func dmChannelIncludesUser(channel, userID string) bool {
	if userID == "" || !strings.HasPrefix(channel, "dm:") {
		return false
	}
	rest := strings.TrimPrefix(channel, "dm:")
	parts := strings.Split(rest, ":")
	if len(parts) != 2 {
		return false
	}
	return parts[0] == userID || parts[1] == userID
}
