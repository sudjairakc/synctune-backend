package model

import "errors"

// Sentinel errors สำหรับ Business Logic
var (
	ErrSongNotFound        = errors.New("song not found in queue")
	ErrDuplicateSong       = errors.New("song already exists in queue")
	ErrQueueFull           = errors.New("queue has reached maximum size")
	ErrInvalidURL          = errors.New("invalid youtube url")
	ErrInvalidSongID       = errors.New("song_id does not match current song")
	ErrCannotRemoveCurrent = errors.New("cannot remove the currently playing song")
	ErrInvalidErrorCode    = errors.New("error_code must be 101 or 150")
	ErrIndexOutOfRange     = errors.New("new_index is out of range")
)
