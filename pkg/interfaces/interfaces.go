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
	GetMetadata(ctx context.Context, flt models.GetAvatarMetadataFilters) (
		res models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error)
	SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error
	UpdateStatus(ctx context.Context, status models.UpdateAvatarStatus) error
}

type ServiceI interface {
	Save(ctx context.Context, file multipart.File, header *multipart.FileHeader, userID string) (
		models.SaveAvatarResponse, error)
	GetMetadata(ctx context.Context, id uuid.UUID) (models.GetAvatarMetadata, error)
	SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error
	UploadThumbnailsToS3(ctx context.Context, msg models.Message) error
	Get(ctx context.Context, flt models.GetAvatarMetadataFilters, dimensions string) (
		res models.GetAvatarResponse, err error)
}

type S3I interface {
	Save(ctx context.Context, objectKey string, reader io.ReadSeeker) error
	Get(ctx context.Context, objectKey string) ([]byte, error)
	Check() (bool, error)
	GetAddr() string
	GetBucketName() string
}

type BrokerI interface {
	Produce(ctx context.Context, m models.Message) error
	Check() error
}
