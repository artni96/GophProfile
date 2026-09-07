package models

import (
	"time"
	"uuid"
)

type SaveAvatarRequest struct {
	ID               uuid.UUID
	UserID           string `json:"user_id"`
	FileName         string `json:"file_name"`
	MimeType         string `json:"mime_type"`
	SizeBytes        uint64 `json:"size_bytes"`
	S3Key            string `json:"s3_key"`
	UploadStatus     string `json:"upload_status"`
	ProcessingStatus string `json:"processing_status"`
	Height           uint64
	Width            uint64
}

type SaveAvatarResponse struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	Status    string    `json:"status" db:"upload_status"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	URL       string    `json:"url"`
}

type Dimensions struct {
	Height uint64 `json:"height"`
	Width  uint64 `json:"width"`
}

type AvatarDBEntity struct {
	ID        uuid.UUID  `db:"id"`
	UserID    string     `db:"user_id"`
	FileName  string     `db:"file_name"`
	MimeType  string     `db:"mime_type"`
	Size      uint64     `db:"size_bytes"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	Height    uint64     `db:"height"`
	Width     uint64     `db:"width"`
}

type GetAvatarResponse struct {
	ID         uuid.UUID  `json:"id"`
	UserID     string     `json:"user_id"`
	FileName   string     `json:"file_name"`
	MimeType   string     `json:"mime_type"`
	Size       uint64     `json:"size"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Dimensions Dimensions `json:"dimensions"`
}

type SaveThumbnail struct {
	AvatarID   uuid.UUID `db:"avatar_id"`
	S3Key      string    `db:"s3_key"`
	Dimensions string    `db:"dimensions"`
}

type UpdateAvatarStatus struct {
	AvatarID         uuid.UUID `db:"avatar_id"`
	UploadStatus     string    `db:"upload_status"`
	ProcessingStatus string    `db:"processing_status"`
	UpdatedAt        time.Time `db:"updated_at"`
}
