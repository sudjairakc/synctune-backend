package model

// User แทน identity ของ Client แต่ละคน (ephemeral per session)
type User struct {
	ID         string `json:"id"`          // UUID ต่อ session — สร้างโดย server
	Username   string `json:"username"`
	ProfileImg string `json:"profile_img"` // URL หรือ "" ถ้าไม่มี
}

// ChatMessage แทนข้อความแชท 1 รายการ
type ChatMessage struct {
	ID        string `json:"id"`        // UUID ต่อข้อความ
	User      User   `json:"user"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"` // Unix milliseconds
}
