package model

// Presence แทนตำแหน่งและสถานะของผู้ใช้บน office map
type Presence struct {
	ConnectionID string  `json:"connection_id"`
	UserID       string  `json:"user_id"`
	Username     string  `json:"username"`
	ProfileImg   string  `json:"profile_img"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Dir          string  `json:"dir"` // "up"|"down"|"left"|"right"
	ZoneID       string  `json:"zone_id"`
	BubbleID     string  `json:"bubble_id,omitempty"`
	FollowingID  string  `json:"following_id,omitempty"`
}

// PrivateZoneState คือ occupants + invites ของ private zone ในห้อง (key = connection_id)
type PrivateZoneState struct {
	Occupants []string `json:"occupants"`
	Invites   []string `json:"invites"`
}
