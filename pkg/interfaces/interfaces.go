package interfaces

import (
	"context"
	"io"
	"mime/multipart"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	_ "github.com/golang/mock/mockgen/model"
	"github.com/jmoiron/sqlx"
)

//go:generate
type RepositoryI interface {
	Save(ctx context.Context, toUpload models.SaveAvatarRequest) (models.SaveAvatarResponse, error)
	GetMetadata(ctx context.Context, flt models.AvatarFilters) (
		res models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error)
	GetMetadataList(ctx context.Context, flt models.AvatarFilters) (
		originals []models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error)
	SaveThumbnail(ctx context.Context, tx *sqlx.Tx, t models.SaveThumbnail) error
	UpdateStatus(ctx context.Context, tx *sqlx.Tx, status models.UpdateAvatarStatus) error
	DeleteAvatarWithThumbnails(tx *sqlx.Tx, flt models.AvatarFilters) ([]string, error)
	BeginTx(ctx context.Context) (*sqlx.Tx, error)
	CommitTx(tx *sqlx.Tx) error
	RollbackTx(tx *sqlx.Tx) error
}

type ServiceI interface {
	Save(ctx context.Context, file multipart.File, header *multipart.FileHeader, userID string) (
		models.SaveAvatarResponse, error)
	GetMetadata(ctx context.Context, id uuid.UUID) (models.GetAvatarMetadata, error)
	UploadThumbnailsToS3(ctx context.Context, msg models.Message) error
	Get(ctx context.Context, flt models.AvatarFilters, dimensions string) (
		res models.GetAvatarResponse, err error)
	Delete(ctx context.Context, flt models.AvatarFilters) error
	DeleteFromS3(ctx context.Context, avatarID uuid.UUID) error
	GetMetadataList(ctx context.Context, flt models.AvatarFilters) (res models.GetUserAvatarsListResponse, err error)
}

//go:generate
type S3I interface {
	Save(ctx context.Context, objectKey string, reader io.ReadSeeker) error
	Get(ctx context.Context, objectKey string) ([]byte, error)
	Check() (bool, error)
	GetAddr() string
	GetBucketName() string
	Delete(ctx context.Context, objectKey string) error
}

//go:generate
type BrokerI interface {
	Produce(ctx context.Context, m models.Message) error
	Check() error
}
