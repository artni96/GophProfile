package models

import "uuid"

type ActionType string

const (
	Upload ActionType = "upload"
	Remove ActionType = "remove"
)

type Message struct {
	ID       uuid.UUID
	AvatarID uuid.UUID
	UserID   string
	Body     []byte
	Action   ActionType
	S3Key    string
}
