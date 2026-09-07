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
	"github.com/artni96/GophProfile/internal/worker"
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
	broker   *worker.Broker
}

func NewService(repo interfaces.RepositoryI, logger *zap.Logger, s3Client interfaces.S3I, broker *worker.Broker) *Service {
	serv := &Service{repo: repo, logger: logger, s3Client: s3Client, broker: broker}
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

	err = s.broker.Produce(ctx, brokerMessage)
	if err != nil {
		return res, fmt.Errorf("failed to send message to the broker: %w", err)
	}
	res.URL = fmt.Sprintf("%s/%s/%s", s.s3Client.GetAddr(), s.s3Client.GetBucketName(), entityToSave.S3Key)
	return res, nil
}

func (s *Service) GetMetadata(ctx context.Context, id uuid.UUID) (res models.GetAvatarMetadata, err error) {
	original, thumbnails, err := s.repo.GetMetadata(ctx, id)
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

func (s *Service) Get(ctx context.Context, id uuid.UUID, dimensions string) (res []byte, err error) {
	md, thumbnails, err := s.repo.GetMetadata(ctx, id)
	if err != nil {
		return nil, err
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
			zap.String("avatar_id", id.String()), zap.String("dimensions", dimensions))
		return nil, avatars.ErrAvatarNotFound
	}
	res, err = s.s3Client.Get(ctx, s3key)
	if err != nil {
		s.logger.Debug("failed to fetch me",
			zap.String("avatar_id", id.String()),
			zap.String("dimensions", dimensions), zap.Error(err))
	}
	return res, err
}

func (s *Service) SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error {
	err := s.repo.SaveThumbnail(ctx, t)
	if err != nil {
		return err
	}
	return nil
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
		err = s.repo.SaveThumbnail(ctx, t)
		if err != nil {
			return err
		}
	}
	err := s.UpdateStatus(ctx, models.UpdateAvatarStatus{
		AvatarID:         msg.AvatarID,
		ProcessingStatus: "uploaded",
		UpdatedAt:        time.Now(),
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) UpdateStatus(ctx context.Context, status models.UpdateAvatarStatus) error {
	err := s.repo.UpdateStatus(ctx, status)
	if err != nil {
		return err
	}
	return nil
}
