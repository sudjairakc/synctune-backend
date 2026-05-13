package controller

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/broadcaster"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

const voteTTL = 30 * time.Second

type voteCastPayload struct {
	VoteID string `json:"vote_id"`
}

// startVote สร้าง vote ใหม่สำหรับ action remove/skip โดย initiator auto-vote yes
// ถ้า yes ถึง required แล้วทันที (ห้องคนเดียว) จะ execute แล้ว return true, nil
// ถ้าสร้าง vote แล้วรอ return false, nil
func startVote(h *hub.Hub, client *hub.Client, action model.VoteAction, songQueueID, songTitle string) (executed bool, err error) {
	ctx := context.Background()
	roomID := client.RoomID

	existing, err := h.Store().GetVote(ctx, roomID)
	if err != nil {
		return false, err
	}
	if existing != nil {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "VOTE_IN_PROGRESS", Message: "มีการโหวตอยู่แล้ว กรุณารอให้เสร็จก่อน"})
		return false, nil
	}

	online := h.OnlineUsersInRoom(roomID)
	total := len(online)
	if total == 0 {
		total = 1
	}

	now := time.Now()
	vote := &model.Vote{
		ID:            uuid.New().String(),
		Action:        action,
		SongQueueID:   songQueueID,
		SongTitle:     songTitle,
		InitiatedBy:   client.User.Username,
		InitiatedByID: client.User.ID,
		YesVoterIDs:   []string{client.User.ID},
		TotalAtStart:  total,
		ExpiresAt:     now.Add(voteTTL).UnixMilli(),
	}

	if len(vote.YesVoterIDs) >= vote.Required() {
		return true, nil
	}

	if err := h.Store().SetVote(ctx, roomID, vote, voteTTL); err != nil {
		return false, err
	}

	log.Info().Str("vote_id", vote.ID).Str("room_id", roomID).Str("action", string(action)).Str("song_title", songTitle).Str("initiated_by", client.User.Username).Int("total", total).Int("required", vote.Required()).Msg("vote started")
	broadcaster.BroadcastVoteStarted(h, roomID, vote)
	return false, nil
}

// HandleVoteCast จัดการ event vote_cast — เพิ่ม yes vote และตรวจว่าผ่านหรือยัง
func HandleVoteCast(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if !requireJoined(h, client) {
		return
	}

	var payload voteCastPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.VoteID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "INVALID_MESSAGE", Message: "รูปแบบ Payload ไม่ถูกต้อง"})
		return
	}

	ctx := context.Background()
	roomID := client.RoomID

	vote, err := h.Store().GetVote(ctx, roomID)
	if err != nil {
		log.Error().Err(err).Msg("HandleVoteCast: failed to get vote")
		return
	}
	if vote == nil || vote.ID != payload.VoteID {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NO_ACTIVE_VOTE", Message: "ไม่มีการโหวตที่ active อยู่"})
		return
	}
	if vote.HasVoted(client.User.ID) {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "ALREADY_VOTED", Message: "โหวตแล้ว"})
		return
	}

	vote.YesVoterIDs = append(vote.YesVoterIDs, client.User.ID)
	log.Info().Str("vote_id", vote.ID).Str("room_id", roomID).Str("username", client.User.Username).Int("yes_votes", len(vote.YesVoterIDs)).Int("required", vote.Required()).Msg("vote cast")

	if len(vote.YesVoterIDs) >= vote.Required() {
		_ = h.Store().DeleteVote(ctx, roomID)
		broadcaster.BroadcastVoteResolved(h, roomID, vote, "passed")
		resolveVote(h, client, vote)
		return
	}

	if err := h.Store().SetVote(ctx, roomID, vote, voteTTL); err != nil {
		log.Error().Err(err).Msg("HandleVoteCast: failed to update vote")
		return
	}
	broadcaster.BroadcastVoteUpdated(h, roomID, vote)
}

// resolveVote execute action หลัง vote ผ่าน
func resolveVote(h *hub.Hub, client *hub.Client, vote *model.Vote) {
	switch vote.Action {
	case model.VoteActionRemoveSong:
		executeRemoveSong(h, client, vote.SongQueueID)
	case model.VoteActionSkipSong:
		executeSkipSong(h, client, vote.SongQueueID)
	}
}
