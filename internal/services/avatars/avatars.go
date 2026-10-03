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
	"log/slog"
	"mime/multipart"
	"time"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/deepteams/webp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	logger   *slog.Logger
	s3Client interfaces.S3I
	Broker   interfaces.BrokerI
	tracer   trace.Tracer
}

func NewService(repo interfaces.RepositoryI, logger *slog.Logger, s3Client interfaces.S3I, broker interfaces.BrokerI, tracer trace.Tracer) *Service {
	serv := &Service{repo: repo, logger: logger, s3Client: s3Client, Broker: broker, tracer: tracer}
	return serv
}

func (s *Service) Save(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
	userID string,
) (res models.SaveAvatarResponse, err error) {
	ctx, span := s.tracer.Start(ctx, "avatars.Save")
	defer span.End()

	if header.Size > imageMaxSize {
		span.RecordError(ErrExceededSize)
		span.SetStatus(codes.Error, "file too large")
		return res, fmt.Errorf("%w: file size - %d", ErrExceededSize, header.Size)
	}
	var mimeType string
	for k, v := range header.Header {
		if k == "Content-Type" {
			mimeType = v[0]
		}
	}
	body, err := io.ReadAll(file)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, errors.Join(fmt.Errorf("failed to read body: %w", err), ErrInternalServer)
	}
	fileCfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, errors.Join(fmt.Errorf("failed to decode body: %w", err), ErrInternalServer)
	}

	isFormatSupported := false
	for _, supportedFormat := range supportedFormats {
		if format == supportedFormat {
			isFormatSupported = true
			break
		}
	}

	if !isFormatSupported {
		span.RecordError(ErrUnsupportedFormat)
		span.SetStatus(codes.Error, fmt.Sprintf("current format: %s", format))
		return res, fmt.Errorf("%w, file format - %s", ErrUnsupportedFormat, format)
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, err
	}
	entityToSave.UploadStatus = "uploaded"
	res, err = s.repo.Save(ctx, entityToSave)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, fmt.Errorf("failed to send message to the broker: %w", err)
	}
	res.URL = fmt.Sprintf("%s/%s/%s", s.s3Client.GetAddr(), s.s3Client.GetBucketName(), entityToSave.S3Key)
	span.SetStatus(codes.Ok, "")
	return res, nil
}

func (s *Service) GetMetadata(ctx context.Context, id uuid.UUID) (res models.GetAvatarMetadata, err error) {
	ctx, span := s.tracer.Start(ctx, "avatars.GetMetadata")
	defer span.End()

	span.SetAttributes(attribute.String("avatars.id", id.String()))
	flt := models.AvatarFilters{
		ID: id,
	}
	original, thumbnails, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
	span.SetStatus(codes.Ok, "")
	return res, nil
}

func (s *Service) GetMetadataList(ctx context.Context, flt models.AvatarFilters) (
	res models.GetUserAvatarsListResponse, err error) {
	ctx, span := s.tracer.Start(ctx, "avatars.GetMetadataList")
	defer span.End()
	span.SetAttributes(attribute.String("avatar_id", flt.ID.String()))

	originals, thumbnails, err := s.repo.GetMetadataList(ctx, flt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
	span.SetStatus(codes.Ok, "")
	return res, nil
}

func (s *Service) Get(ctx context.Context, flt models.AvatarFilters, dimensions string) (
	res models.GetAvatarResponse, err error) {
	ctx, span := s.tracer.Start(ctx, "Retrieving image")
	span.SetAttributes(attribute.String("dimensions", dimensions))

	defer span.End()
	md, thumbnails, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		span.RecordError(avatars.ErrAvatarNotFound)
		span.SetStatus(codes.Error, avatars.ErrAvatarNotFound.Error())
		return res, errors.Join(
			fmt.Errorf("failed to get metadata: avatar_id - %s, dimension - %s", md.ID.String(), dimensions),
			avatars.ErrAvatarNotFound,
		)
	}
	binary, err := s.s3Client.Get(ctx, s3key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, errors.Join(
			fmt.Errorf("failed to fetch binary data from s3: %w", err), avatars.ErrAvatarNotFound)
	}
	res.Binary = binary
	res.MimeType = md.MimeType
	span.SetStatus(codes.Ok, "")
	return res, err
}

func (s *Service) UploadThumbnailsToS3(ctx context.Context, msg models.Message) error {
	ctx, span := s.tracer.Start(ctx, "avatars.UploadThumbnailsToS3")
	defer span.End()

	span.SetAttributes(
		attribute.String("id", msg.ID.String()),
		attribute.String("user_id", msg.UserID),
		attribute.String("avatar_id", msg.AvatarID.String()),
		attribute.String("s3_key", msg.S3Key),
	)
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	for _, dim := range dimensions {
		img, format, err := image.Decode(bytes.NewReader(msg.Body))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
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
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to encode image: %w", err)
		}
		strDimensions := fmt.Sprintf("%dx%d", dim.Width, dim.Height)
		s3key := fmt.Sprintf("user%s/%s/%s", msg.UserID, strDimensions, msg.AvatarID.String())
		err = s.s3Client.Save(ctx, s3key, bytes.NewReader(formated.Bytes()))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to upload image: %w", err)
		}

		t := models.SaveThumbnail{
			msg.AvatarID,
			s3key,
			strDimensions,
		}
		err = s.repo.SaveThumbnail(ctx, tx, t)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	err = s.repo.UpdateStatus(ctx, tx, models.UpdateAvatarStatus{
		AvatarID:         msg.AvatarID,
		ProcessingStatus: "uploaded",
		UpdatedAt:        time.Now(),
	})

	if err = s.repo.CommitTx(tx); err != nil {
		err = s.repo.RollbackTx(tx)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (s *Service) Delete(ctx context.Context, flt models.AvatarFilters) error {
	ctx, span := s.tracer.Start(ctx, "avatars.Service.Delete")
	defer span.End()

	original, _, err := s.repo.GetMetadata(ctx, flt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to send message to the broker: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (s *Service) DeleteFromS3(ctx context.Context, avatarID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "avatars.Service.DeleteFromS3")
	defer span.End()

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	s3keys, err := s.repo.DeleteAvatarWithThumbnails(ctx, tx, models.AvatarFilters{ID: avatarID})
	if err != nil {
		err = s.repo.RollbackTx(tx)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	for _, s3key := range s3keys {
		err = s.s3Client.Delete(ctx, s3key)
		if err != nil {
			err = tx.Rollback()
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("failed to rollback transaction: %w", err)
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	err = s.repo.CommitTx(tx)
	if err != nil {
		err = s.repo.RollbackTx(tx)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to rollback transaction: %w", err)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}
