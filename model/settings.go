package model

// AppSettings คือ global settings ของแอป (เก็บใน Redis key เดียว)
type AppSettings struct {
	AllowSkipBroadcast bool `json:"allow_skip_broadcast"`
}
