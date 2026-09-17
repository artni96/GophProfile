package avatars

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"time"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/deepteams/webp"
	"go.uber.org/zap"
	"golang.org/x/image/draw"
)

const (
	imageMaxSize = 10 * 1024 * 1024
)

var (
	ErrExceededSize      = errors.New("size is too large")
	ErrUnsupportedFormat = errors.New("unsupported format")
	ErrInternalServer    = errors.New("internal server error")
	supportedFormats     = []string{"jpeg", "png", "webp"}
)

type Service struct {
	repo     interfaces.RepositoryI
	logger   *zap.Logger
	s3Client interfaces.S3I
	Broker   interfaces.BrokerI
}

func NewService(repo interfaces.RepositoryI, logger *zap.Logger, s3Client interfaces.S3I, broker interfaces.BrokerI) *Service {
	serv := &Service{repo: repo, logger: logger, s3Client: s3Client, Broker: broker}
	return serv
}

func (s *Service) Save(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
	userID string,
) (res models.SaveAvatarResponse, err error) {
	if header.Size > imageMaxSize {
		s.logger.Debug("image too big", zap.Int64("file size", header.Size))
		return res, ErrExceededSize
	}
	var mimeType string
	for k, v := range header.Header {
		if k == "Content-Type" {
			mimeType = v[0]
		}
	}
	body, err := io.ReadAll(file)
	if err != nil {
		s.logger.Debug("failed to read body", zap.Error(err))
		return res, ErrInternalServer
	}
	fileCfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		s.logger.Debug("failed to decode body", zap.Error(err))
		return res, ErrInternalServer
	}

	isFormatSupported := false
	for _, supportedFormat := range supportedFormats {
		if format == supportedFormat {
			isFormatSupported = true
			break
		}
	}

	if !isFormatSupported {
		s.logger.Debug("unsupported file format", zap.String("format", mimeType))
		return res, ErrUnsupportedFormat
	}

	entityToSave := models.SaveAvatarRequest{}

	entityToSave.ID = uuid.New()
	entityToSave.UserID = userID
	entityToSave.FileName = header.Filename
	entityToSave.MimeType = mimeType
	entityToSave.SizeBytes = uint64(header.Size)
	entityToSave.Height = uint64(fileCfg.Height)
	entityToSave.Width = uint64(fileCfg.Width)
	entityToSave.S3Key = fmt.Sprintf("user%s/originals/%s", entityToSave.UserID, entityToSave.ID.String())
	err = s.s3Client.Save(ctx, entityToSave.S3Key, bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	entityToSave.UploadStatus = "uploaded"
	res, err = s.repo.Save(ctx, entityToSave)
	if err != nil {
		return res, err
	}

	brokerMessage := models.Message{
		AvatarID: res.ID,
		UserID:   userID,
		Body:     body,
		Action:   models.Upload,
		ID:       uuid.New(),
	}

	err = s.Broker.Produce(ctx, brokerMessage)
	if err != nil {
		return res, fmt.Errorf("failed to send message to the broker: %w", err)
	}
	res.URL = fmt.Sprintf("%s/%s/%s", s.s3Client.GetAddr(), s.s3Client.GetBucketName(), entityToSave.S3Key)
	return res, nil
}

func (s *Service) GetMetadata(ctx context.Context, id uuid.UUID) (res models.GetAvatarMetadata, err error) {
	flt := models.AvatarFilters{
		ID: id,
	}
	original, thumbnails, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		return res, err
	}
	dimensions := models.Dimensions{
		Width:  original.Width,
		Height: original.Height,
	}

	res.ID = original.ID
	res.UserID = original.UserID
	res.FileName = original.FileName
	res.MimeType = original.MimeType
	res.Size = original.Size
	res.Dimensions = dimensions
	res.CreatedAt = original.CreatedAt

	for i := range thumbnails {
		thumbnails[i].S3Key = fmt.Sprintf(
			"%s/%s/%s", s.s3Client.GetAddr(), s.s3Client.GetBucketName(), thumbnails[i].S3Key)
	}
	res.Thumbnails = thumbnails
	if original.UpdatedAt != nil {
		res.UpdatedAt = *original.UpdatedAt
	}
	return res, nil
}

func (s *Service) GetMetadataList(ctx context.Context, flt models.AvatarFilters) (
	res models.GetUserAvatarsListResponse, err error) {
	originals, thumbnails, err := s.repo.GetMetadataList(ctx, flt)
	if err != nil {
		return res, err
	}
	thumbnailsMd := map[uuid.UUID][]models.GetThumbnailMetadata{}
	for _, thumbnail := range thumbnails {
		thumbnail.S3Key = fmt.Sprintf("%s/%s/%s", s.s3Client.GetAddr(), s.s3Client.GetBucketName(), thumbnail.S3Key)
		thumbnailsMd[thumbnail.AvatarID] = append(thumbnailsMd[thumbnail.AvatarID], thumbnail)
	}
	avatarsMd := make([]models.GetAvatarMetadata, 0, len(originals))
	for _, original := range originals {
		i := models.GetAvatarMetadata{}
		i.ID = original.ID
		i.FileName = original.FileName
		i.MimeType = original.MimeType
		i.Size = original.Size
		i.CreatedAt = original.CreatedAt
		i.UpdatedAt = *original.UpdatedAt
		dimensions := models.Dimensions{
			Width:  original.Width,
			Height: original.Height,
		}
		i.Dimensions = dimensions
		i.Thumbnails = thumbnailsMd[i.ID]
		avatarsMd = append(avatarsMd, i)
	}
	res.Avatars = avatarsMd
	return res, nil
}

func (s *Service) Get(ctx context.Context, flt models.AvatarFilters, dimensions string) (
	res models.GetAvatarResponse, err error) {
	md, thumbnails, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		return res, err
	}
	var s3key string
	if dimensions == "" || dimensions == "original" {
		s3key = md.S3Key
	} else {
		for _, thumbnail := range thumbnails {
			if thumbnail.Dimensions == dimensions {
				s3key = thumbnail.S3Key
			}
		}
	}
	if s3key == "" {
		s.logger.Debug("failed to get metadata",
			zap.String("avatar_id", md.ID.String()), zap.String("dimensions", dimensions))
		return res, avatars.ErrAvatarNotFound
	}
	binary, err := s.s3Client.Get(ctx, s3key)
	if err != nil {
		s.logger.Debug("failed to fetch me",
			zap.String("avatar_id", md.ID.String()),
			zap.String("dimensions", dimensions), zap.Error(err))
	}
	res.Binary = binary
	res.MimeType = md.MimeType
	return res, err
}

func (s *Service) UploadThumbnailsToS3(ctx context.Context, msg models.Message) error {
	var dimensions []models.Dimensions
	dimensions = append(dimensions, models.Dimensions{
		Height: 100,
		Width:  100,
	})
	dimensions = append(dimensions, models.Dimensions{
		Height: 300,
		Width:  300,
	})
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		s.logger.Debug("failed to begin transaction", zap.Error(err))
		return fmt.Errorf("%w", err)
	}
	for _, dim := range dimensions {
		img, format, err := image.Decode(bytes.NewReader(msg.Body))
		if err != nil {
			return err
		}
		dst := image.NewRGBA(image.Rect(0, 0, int(dim.Width), int(dim.Height)))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		formated := new(bytes.Buffer)
		switch format {
		case "png":
			err = png.Encode(formated, dst)
		case "jpeg":
			err = jpeg.Encode(formated, dst, nil)
		case "wepb":
			err = webp.Encode(formated, dst, nil)
		}
		if err != nil {

			s.logger.Debug("failed to encode image", zap.Error(err))
			return fmt.Errorf("failed to encode image: %w", err)
		}
		strDimensions := fmt.Sprintf("%dx%d", dim.Width, dim.Height)
		s3key := fmt.Sprintf("user%s/%s/%s", msg.UserID, strDimensions, msg.AvatarID.String())
		err = s.s3Client.Save(ctx, s3key, bytes.NewReader(formated.Bytes()))
		if err != nil {
			return fmt.Errorf("failed to upload image: %w", err)
		}

		t := models.SaveThumbnail{
			msg.AvatarID,
			s3key,
			strDimensions,
		}
		err = s.repo.SaveThumbnail(ctx, tx, t)
		if err != nil {
			return err
		}
	}
	err = s.repo.UpdateStatus(ctx, tx, models.UpdateAvatarStatus{
		AvatarID:         msg.AvatarID,
		ProcessingStatus: "uploaded",
		UpdatedAt:        time.Now(),
	})

	if err = s.repo.CommitTx(tx); err != nil {
		s.logger.Error("failed to commit transaction", zap.Error(err))
		err = s.repo.RollbackTx(tx)
		if err != nil {
			s.logger.Error("failed to rollback transaction", zap.Error(err))
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, flt models.AvatarFilters) error {
	original, _, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		return err
	}
	brokerMessage := models.Message{
		UserID:   flt.UserID,
		Action:   models.Remove,
		ID:       uuid.New(),
		AvatarID: original.ID,
	}
	err = s.Broker.Produce(ctx, brokerMessage)
	if err != nil {
		s.logger.Debug("failed to delete broker message", zap.Error(err))
		return fmt.Errorf("failed to send message to the broker: %w", err)
	}
	return nil
}

func (s *Service) DeleteFromS3(ctx context.Context, avatarID uuid.UUID) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		s.logger.Error("failed to begin transaction", zap.Error(err))
		return fmt.Errorf("%w", err)
	}
	s3keys, err := s.repo.DeleteAvatarWithThumbnails(tx, models.AvatarFilters{
		ID: avatarID,
	})
	if err != nil {
		s.logger.Error("failed to delete avatar from db", zap.Error(err))
		err = s.repo.RollbackTx(tx)
		if err != nil {
			s.logger.Error("failed to rollback transaction", zap.Error(err))
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
		return err
	}
	for _, s3key := range s3keys {
		err = s.s3Client.Delete(ctx, s3key)
		if err != nil {
			err = tx.Rollback()
			if err != nil {
				s.logger.Error("failed to rollback transaction", zap.Error(err))
				return fmt.Errorf("failed to rollback transaction: %w", err)
			}
			return err
		}
	}
	err = s.repo.CommitTx(tx)
	if err != nil {
		s.logger.Error("failed to commit transaction", zap.Error(err))
		err = s.repo.RollbackTx(tx)
		if err != nil {
			s.logger.Error("failed to rollback transaction", zap.Error(err))
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
	}
	s.logger.Debug("successfully deleted avatar", zap.String("avatar_id", avatarID.String()))
	return nil
}
