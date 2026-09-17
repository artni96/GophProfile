package avatars

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/textproto"
	"testing"
	"time"
	"uuid"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/golang/mock/gomock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 500, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 500; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: 128,
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	assert.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

type inMemoryFile struct {
	*bytes.Reader
}

func (f *inMemoryFile) Close() error { return nil }

func newFile(b []byte) multipart.File {
	return &inMemoryFile{Reader: bytes.NewReader(b)}
}

func newFileHeader(filename, contentType string, size int64) *multipart.FileHeader {
	return &multipart.FileHeader{
		Filename: filename,
		Size:     size,
		Header: textproto.MIMEHeader{
			"Content-Type": []string{contentType},
		},
	}
}
func TestSave(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)

	mockUser := "user-1"
	mockStatus := "uploaded"

	header := newFileHeader("avatar.png", "image/png", int64(len(testPNG(t))))
	file := newFile(testPNG(t))

	s3.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	s3.EXPECT().GetAddr().AnyTimes().Return("http://localhost:9000")
	s3.EXPECT().GetBucketName().AnyTimes().Return("test-bucket")
	repo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req models.SaveAvatarRequest) (models.SaveAvatarResponse, error) {
			assert.Equal(t, mockUser, req.UserID)
			assert.Equal(t, "image/png", req.MimeType)
			assert.Equal(t, mockStatus, req.UploadStatus)
			return models.SaveAvatarResponse{ID: uuid.New(), Status: mockStatus, UserID: mockUser}, nil
		})
	broker.EXPECT().Produce(gomock.Any(), gomock.Any()).Return(nil)

	res, err := s.Save(context.Background(), file, header, mockUser)
	assert.NoError(t, err)
	assert.Equal(t, mockStatus, res.Status)
}

func TestGetMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	avatarID := uuid.New()
	repoResp := models.AvatarDBEntity{
		ID:        avatarID,
		UserID:    "user-1",
		FileName:  "test.jpg",
		MimeType:  "image/jpeg",
		Size:      1024,
		CreatedAt: time.Now(),
		UpdatedAt: nil,
		Height:    123,
		Width:     123,
		S3Key:     "test.jpg",
	}
	repo.EXPECT().GetMetadata(ctx, gomock.Any()).Return(
		repoResp,
		[]models.GetThumbnailMetadata{
			{
				AvatarID:   avatarID,
				Dimensions: "100x100",
				S3Key:      "test.jpg",
			},
			{
				AvatarID:   avatarID,
				Dimensions: "300x300",
				S3Key:      "test.jpg",
			},
		}, nil,
	)
	s3.EXPECT().GetAddr().AnyTimes().Return("http://localhost:9000")
	s3.EXPECT().GetBucketName().AnyTimes().Return("test-bucket")
	res, err := s.GetMetadata(ctx, uuid.New())
	assert.NoError(t, err)
	assert.Equal(t, res.ID, avatarID)
	assert.Equal(t, res.UserID, repoResp.UserID)
	assert.Equal(t, res.FileName, repoResp.FileName)
	assert.Equal(t, res.MimeType, repoResp.MimeType)
	assert.Equal(t, res.Dimensions, models.Dimensions{
		repoResp.Height, repoResp.Width,
	})
	assert.Len(t, res.Thumbnails, 2)

}

func TestGetMetadataList(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	updatedAt := time.Now()
	repoRespOriginals := []models.AvatarDBEntity{
		{
			ID:        uuid.New(),
			UserID:    "1",
			FileName:  "test-1.jpg",
			MimeType:  "image/jpeg",
			Size:      1024,
			CreatedAt: time.Now(),
			UpdatedAt: &updatedAt,
			Height:    123,
			Width:     123,
			S3Key:     "test-1.jpg",
		},
		{
			ID:        uuid.New(),
			UserID:    "1",
			FileName:  "test-2.jpg",
			MimeType:  "image/jpeg",
			Size:      2028,
			CreatedAt: time.Now(),
			UpdatedAt: &updatedAt,
			Height:    246,
			Width:     246,
			S3Key:     "test-2.jpg",
		},
	}
	repoRespThumbnails := []models.GetThumbnailMetadata{
		{
			AvatarID:   repoRespOriginals[0].ID,
			Dimensions: "100x100",
			S3Key:      repoRespOriginals[0].S3Key,
		},
		{
			AvatarID:   repoRespOriginals[0].ID,
			Dimensions: "300x300",
			S3Key:      repoRespOriginals[0].S3Key,
		},
		{
			AvatarID:   repoRespOriginals[1].ID,
			Dimensions: "100x100",
			S3Key:      repoRespOriginals[1].S3Key,
		},
		{
			AvatarID:   repoRespOriginals[1].ID,
			Dimensions: "300x300",
			S3Key:      repoRespOriginals[1].S3Key,
		},
	}
	s3.EXPECT().GetAddr().AnyTimes().Return("http://localhost:9000")
	s3.EXPECT().GetBucketName().AnyTimes().Return("test-bucket")
	repo.EXPECT().GetMetadataList(ctx, gomock.Any()).Return(repoRespOriginals, repoRespThumbnails, nil)
	res, err := s.GetMetadataList(ctx, models.AvatarFilters{UserID: "1"})
	assert.NoError(t, err)
	assert.Len(t, res.Avatars, 2)

	assert.Equal(t, repoRespOriginals[0].ID, res.Avatars[0].ID)
	assert.Equal(t, repoRespOriginals[0].FileName, res.Avatars[0].FileName)
	assert.Equal(t, repoRespOriginals[0].MimeType, res.Avatars[0].MimeType)
	assert.Equal(t, repoRespOriginals[0].Size, res.Avatars[0].Size)
	assert.Equal(t, repoRespOriginals[0].CreatedAt, res.Avatars[0].CreatedAt)
	assert.Equal(t, repoRespOriginals[0].UpdatedAt, &res.Avatars[0].UpdatedAt)
	assert.Len(t, res.Avatars[0].Thumbnails, 2)

	assert.Equal(t, repoRespOriginals[1].ID, res.Avatars[1].ID)
	assert.Equal(t, repoRespOriginals[1].FileName, res.Avatars[1].FileName)
	assert.Equal(t, repoRespOriginals[1].MimeType, res.Avatars[1].MimeType)
	assert.Equal(t, repoRespOriginals[1].Size, res.Avatars[1].Size)
	assert.Equal(t, repoRespOriginals[1].CreatedAt, res.Avatars[1].CreatedAt)
	assert.Equal(t, repoRespOriginals[1].UpdatedAt, &res.Avatars[1].UpdatedAt)
	assert.Len(t, res.Avatars[1].Thumbnails, 2)
}

func TestGet(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	s3key := "test.jpg"
	avatarID := uuid.New()
	mimeType := "image/jpeg"
	repoResp := models.AvatarDBEntity{
		ID:        avatarID,
		UserID:    "1",
		FileName:  "test.jpg",
		MimeType:  mimeType,
		Size:      1024,
		CreatedAt: time.Now(),
		UpdatedAt: nil,
		Height:    123,
		Width:     123,
		S3Key:     s3key,
	}
	repo.EXPECT().GetMetadata(ctx, gomock.Any()).Return(
		repoResp,
		[]models.GetThumbnailMetadata{
			{
				AvatarID:   avatarID,
				Dimensions: "100x100",
				S3Key:      s3key,
			},
			{
				AvatarID:   avatarID,
				Dimensions: "300x300",
				S3Key:      s3key,
			},
		}, nil,
	)
	s3.EXPECT().GetAddr().AnyTimes().Return("http://localhost:9000")
	s3.EXPECT().GetBucketName().AnyTimes().Return("test-bucket")
	s3.EXPECT().Get(ctx, gomock.Any()).Return(testPNG(t), nil)

	res, err := s.Get(ctx, models.AvatarFilters{ID: avatarID}, "100x100")
	assert.NoError(t, err)
	assert.Equal(t, res.Binary, testPNG(t))
	assert.Equal(t, res.MimeType, mimeType)
}

func TestUploadThumbnailsToS3(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	avatarID := uuid.New()
	s3key := "test.png"
	db, sqlMock, err := sqlmock.New()
	assert.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	sqlMock.ExpectBegin()
	tx, err := sqlxDB.Beginx()
	assert.NoError(t, err)
	s3.EXPECT().Save(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().BeginTx(gomock.Any()).Return(tx, nil)
	repo.EXPECT().SaveThumbnail(gomock.Any(), tx, gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().UpdateStatus(gomock.Any(), tx, gomock.Any()).Return(nil)
	repo.EXPECT().CommitTx(gomock.Any()).Return(nil)
	msg := models.Message{
		AvatarID: avatarID,
		ID:       uuid.New(),
		Body:     testPNG(t),
		Action:   "upload",
		S3Key:    s3key,
		UserID:   "1",
	}
	err = s.UploadThumbnailsToS3(ctx, msg)
	assert.NoError(t, err)
}

func TestDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	broker.EXPECT().Produce(ctx, gomock.Any()).Return(nil)
	avatarID := uuid.New()
	repoResp := models.AvatarDBEntity{
		ID:        avatarID,
		UserID:    "user-1",
		FileName:  "test.jpg",
		MimeType:  "image/jpeg",
		Size:      1024,
		CreatedAt: time.Now(),
		UpdatedAt: nil,
		Height:    123,
		Width:     123,
		S3Key:     "test.jpg",
	}
	repo.EXPECT().GetMetadata(ctx, gomock.Any()).Return(
		repoResp,
		[]models.GetThumbnailMetadata{
			{
				AvatarID:   avatarID,
				Dimensions: "100x100",
				S3Key:      "test.jpg",
			},
			{
				AvatarID:   avatarID,
				Dimensions: "300x300",
				S3Key:      "test.jpg",
			},
		}, nil,
	)
	err := s.Delete(ctx, models.AvatarFilters{ID: uuid.New()})
	assert.NoError(t, err)
}

func TestDeleteFromS3(t *testing.T) {
	ctrl := gomock.NewController(t)
	s3 := interfaces.NewMockS3I(ctrl)
	repo := interfaces.NewMockRepositoryI(ctrl)
	broker := interfaces.NewMockBrokerI(ctrl)
	s := NewService(repo, zaptest.NewLogger(t), s3, broker)
	ctx := t.Context()
	avatarID := uuid.New()
	db, sqlMock, err := sqlmock.New()
	assert.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	sqlMock.ExpectBegin()
	tx, err := sqlxDB.Beginx()
	assert.NoError(t, err)
	s3.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	repo.EXPECT().BeginTx(gomock.Any()).Return(tx, nil)
	repo.EXPECT().CommitTx(gomock.Any()).Return(nil)
	repo.EXPECT().DeleteAvatarWithThumbnails(tx, models.AvatarFilters{ID: avatarID}).Return(
		[]string{"key1", "key2", "key3"}, nil)
	err = s.DeleteFromS3(ctx, avatarID)
	assert.NoError(t, err)
}
