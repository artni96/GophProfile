package models

type HealthResponse struct {
	Database bool `json:"database"`
	S3       bool `json:"s3"`
	Broker   bool `json:"broker"`
}
