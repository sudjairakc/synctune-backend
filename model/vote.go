package model

// VoteAction คือประเภท action ที่ต้องโหวต
type VoteAction string

const (
	VoteActionRemoveSong    VoteAction = "remove_song"
	VoteActionSkipSong      VoteAction = "skip_song"
	VoteActionSkipBroadcast VoteAction = "skip_broadcast" // ทุกคนต้องโหวต (unanimous)
)

// Vote แทน 1 การโหวตที่กำลัง active ในห้อง
type Vote struct {
	ID            string     `json:"id"`
	Action        VoteAction `json:"action"`
	SongQueueID   string     `json:"song_queue_id"`
	SongTitle     string     `json:"song_title"`
	InitiatedBy   string     `json:"initiated_by"`
	InitiatedByID string     `json:"initiated_by_id"`
	YesVoterIDs   []string   `json:"yes_voter_ids"`
	TotalAtStart  int        `json:"total_at_start"` // จำนวน online users ตอนเริ่มโหวต
	ExpiresAt     int64      `json:"expires_at"`     // Unix milliseconds
}

// Required คืนจำนวน yes votes ขั้นต่ำที่ต้องการ
// skip_broadcast ต้องการ unanimous (ทุกคน), อื่นๆ ใช้ majority (ceil)
func (v *Vote) Required() int {
	if v.Action == VoteActionSkipBroadcast {
		return v.TotalAtStart
	}
	return (v.TotalAtStart + 1) / 2
}

// HasVoted ตรวจว่า userID โหวตแล้วหรือยัง
func (v *Vote) HasVoted(userID string) bool {
	for _, id := range v.YesVoterIDs {
		if id == userID {
			return true
		}
	}
	return false
}
