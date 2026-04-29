package model

// ChatMessageRef แทน preview ของข้อความที่ถูก reply หรือ thread
type ChatMessageRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Text     string `json:"text"` // truncated to 100 chars
	ImageURL string `json:"image_url,omitempty"`
}

// User แทน identity ของ Client แต่ละคน (ephemeral per session)
type User struct {
	ID         string `json:"id"`          // UUID ต่อ session — สร้างโดย server
	Username   string `json:"username"`
	ProfileImg string `json:"profile_img"` // URL หรือ "" ถ้าไม่มี
}

// ChatMessage แทนข้อความแชท 1 รายการ
type ChatMessage struct {
	ID        string              `json:"id"`
	User      User                `json:"user"`
	Text      string              `json:"text"`
	Timestamp int64               `json:"timestamp"` // Unix milliseconds
	Deleted   bool                `json:"deleted,omitempty"`
	ReplyTo   *ChatMessageRef     `json:"reply_to,omitempty"`
	ThreadID  string              `json:"thread_id,omitempty"` // parent message ID (for thread replies)
	Reactions map[string][]string `json:"reactions,omitempty"` // emoji → []userID
	ImageURL  string              `json:"image_url,omitempty"` // base64 data URI or remote URL
}
