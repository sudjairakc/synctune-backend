// Package controller — voice (WebRTC signaling) handlers
package controller

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
	"github.com/synctune/backend/hub"
	"github.com/synctune/backend/model"
)

// voiceStartBroadcast คือ payload ที่ broadcast ไปทุกคนในห้องเมื่อมีคนเริ่ม PTT
type voiceStartBroadcast struct {
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	ProfileImg string `json:"profile_img"`
}

// voiceStopBroadcast คือ payload ที่ broadcast เมื่อปล่อย PTT
type voiceStopBroadcast struct {
	UserID string `json:"user_id"`
}

// voiceJoinPayload — listener ส่งหา speaker เพื่อขอรับ audio
type voiceJoinPayload struct {
	To string `json:"to"`
}

// voiceOfferPayload — speaker ส่ง SDP offer ไปหา listener
type voiceOfferPayload struct {
	To  string `json:"to"`
	SDP string `json:"sdp"`
}

// voiceAnswerPayload — listener ส่ง SDP answer กลับหา speaker
type voiceAnswerPayload struct {
	To  string `json:"to"`
	SDP string `json:"sdp"`
}

// voiceICEPayload — ICE candidate exchange ทั้งสองทิศทาง
type voiceICEPayload struct {
	To        string          `json:"to"`
	Candidate json.RawMessage `json:"candidate"`
}

// voiceForwardWithFrom คือ payload ที่ส่งต่อพร้อม from field
type voiceForwardWithFrom struct {
	From string `json:"from"`
	SDP  string `json:"sdp,omitempty"`
}

// voiceICEForward คือ ICE payload ที่ส่งต่อพร้อม from field
type voiceICEForward struct {
	From      string          `json:"from"`
	Candidate json.RawMessage `json:"candidate"`
}

// HandleVoiceStart broadcast ว่า client นี้กำลัง PTT
func HandleVoiceStart(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" {
		h.SendToSession(client.Conn, "error", model.WSError{Code: "NOT_JOINED", Message: "ต้องส่ง join ก่อน"})
		return
	}
	log.Info().Str("user_id", client.ID).Str("username", client.User.Username).Str("room_id", client.RoomID).Msg("voice PTT start")
	h.BroadcastToRoom(client.RoomID, "voice_start", voiceStartBroadcast{
		UserID:     client.ID,
		Username:   client.User.Username,
		ProfileImg: client.User.ProfileImg,
	})
}

// HandleVoiceStop broadcast ว่า client หยุด PTT แล้ว
func HandleVoiceStop(h *hub.Hub, client *hub.Client, _ json.RawMessage) {
	if client.User.ID == "" {
		return
	}
	log.Info().Str("user_id", client.ID).Str("room_id", client.RoomID).Msg("voice PTT stop")
	h.BroadcastToRoom(client.RoomID, "voice_stop", voiceStopBroadcast{UserID: client.ID})
}

// HandleVoiceJoin route listener → speaker (listener แจ้งว่าพร้อมรับ offer)
func HandleVoiceJoin(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		return
	}
	var payload voiceJoinPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.To == "" {
		return
	}
	h.SendToClient(payload.To, "voice_join", voiceForwardWithFrom{From: client.ID})
}

// HandleVoiceOffer route speaker → listener
func HandleVoiceOffer(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		return
	}
	var payload voiceOfferPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.To == "" {
		return
	}
	h.SendToClient(payload.To, "voice_offer", voiceForwardWithFrom{From: client.ID, SDP: payload.SDP})
}

// HandleVoiceAnswer route listener → speaker
func HandleVoiceAnswer(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		return
	}
	var payload voiceAnswerPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.To == "" {
		return
	}
	h.SendToClient(payload.To, "voice_answer", voiceForwardWithFrom{From: client.ID, SDP: payload.SDP})
}

// HandleVoiceICE route ICE candidate ทั้งสองทิศทาง
func HandleVoiceICE(h *hub.Hub, client *hub.Client, rawPayload json.RawMessage) {
	if client.User.ID == "" {
		return
	}
	var payload voiceICEPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload.To == "" {
		return
	}
	h.SendToClient(payload.To, "voice_ice", voiceICEForward{From: client.ID, Candidate: payload.Candidate})
}
