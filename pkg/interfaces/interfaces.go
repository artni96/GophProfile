package interfaces

import (
	"context"
	"io"
	"mime/multipart"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
)

type RepositoryI interface {
	Save(ctx context.Context, toUpload models.SaveAvatarRequest) (models.SaveAvatarResponse, error)
	GetMetadata(ctx context.Context, id uuid.UUID) (res models.AvatarDBEntity, err error)
	SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error
	UpdateStatus(ctx context.Context, status models.UpdateAvatarStatus) error
}

type ServiceI interface {
	Save(ctx context.Context, file multipart.File, header *multipart.FileHeader, userID string) (
		models.SaveAvatarResponse, error)
	GetMetadata(ctx context.Context, id uuid.UUID) (models.GetAvatarResponse, error)
	SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error
	UploadThumbnailsToS3(ctx context.Context, msg models.Message) error
}

type S3I interface {
	Save(ctx context.Context, objectKey string, reader io.ReadSeeker) error
	Get(ctx context.Context, objectKey string) ([]byte, error)
	GetAddr() string
	GetBucketName() string
}
