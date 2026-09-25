package avatars

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/tests"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

func prepareDeps(t *testing.T) (context.Context, *Repository) {
	tracer := otel.Tracer("testTracer")
	ctx := context.Background()
	deps, err := tests.NewTestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(deps.DB, tracer)
	return ctx, repo
}

func avatarFixture(ctx context.Context, t *testing.T, repo *Repository) models.SaveAvatarResponse {
	body := models.SaveAvatarRequest{
		ID:           uuid.New(),
		UserID:       "1",
		FileName:     "test.png",
		MimeType:     "image/png",
		SizeBytes:    1024,
		S3Key:        "test.png",
		UploadStatus: "pending",
		Height:       500,
		Width:        500,
	}
	entity, err := repo.Save(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

func thumbnailFixtures(
	ctx context.Context, t *testing.T, repo *Repository, avatarID uuid.UUID) {
	tx, err := repo.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = repo.SaveThumbnail(ctx, tx, models.SaveThumbnail{
		AvatarID:   avatarID,
		S3Key:      "test.png",
		Dimensions: "100x100",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.SaveThumbnail(ctx, tx, models.SaveThumbnail{
		AvatarID:   avatarID,
		S3Key:      "test.png",
		Dimensions: "300x300",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
}

func avatarFixturesList(ctx context.Context, t *testing.T, repo *Repository) (res []models.SaveAvatarResponse) {
	for i := 1; i <= 10; i++ {
		body := models.SaveAvatarRequest{
			ID:           uuid.New(),
			UserID:       "1",
			FileName:     fmt.Sprintf("test%d.png", i),
			MimeType:     "image/png",
			SizeBytes:    1024,
			S3Key:        fmt.Sprintf("test%d.png", i),
			UploadStatus: "pending",
			Height:       500,
			Width:        500,
		}
		entity, err := repo.Save(ctx, body)
		if err != nil {
			t.Fatal(err)
		}
		res = append(res, entity)
	}
	return res
}

func TestSave(t *testing.T) {
	ctx, repo := prepareDeps(t)
	avatarID := uuid.New()
	tests := []struct {
		name    string
		body    models.SaveAvatarRequest
		failure bool
	}{
		{
			name: "success",
			body: models.SaveAvatarRequest{
				ID:           avatarID,
				UserID:       "1",
				FileName:     "test.png",
				MimeType:     "image/png",
				SizeBytes:    1024,
				S3Key:        "test.png",
				UploadStatus: "pending",
				Height:       500,
				Width:        500,
			},
			failure: false,
		},
		{
			name: "failure - not unique id",
			body: models.SaveAvatarRequest{
				ID:           avatarID,
				UserID:       "1",
				FileName:     "test.png",
				MimeType:     "image/png",
				SizeBytes:    1024,
				S3Key:        "test.png",
				UploadStatus: "pending",
				Height:       500,
				Width:        500,
			},
			failure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entity, err := repo.Save(ctx, tt.body)
			assert.Equal(t, tt.failure, err != nil)
			if !tt.failure {
				assert.Equal(t, tt.body.UserID, entity.UserID)
				assert.Equal(t, tt.body.ID, entity.ID)
				assert.Equal(t, tt.body.UploadStatus, entity.Status)
				assert.NotNil(t, entity.CreatedAt)
			}
		})
	}
}

func TestGetMetadata(t *testing.T) {
	ctx, repo := prepareDeps(t)
	testAvatar := avatarFixture(ctx, t, repo)
	tests := []struct {
		name    string
		filters models.AvatarFilters
		failure bool
	}{
		{
			name: "success",
			filters: models.AvatarFilters{
				ID: testAvatar.ID,
			},
			failure: false,
		},
		{
			name: "success",
			filters: models.AvatarFilters{
				UserID: testAvatar.UserID,
			},
			failure: false,
		},
		{
			name: "failure - not found",
			filters: models.AvatarFilters{
				ID: uuid.New(),
			},
			failure: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, thumbs, err := repo.GetMetadata(ctx, models.AvatarFilters{ID: testAvatar.ID})
			assert.Equal(t, tt.failure, err != nil)
			if !tt.failure {
				assert.Equal(t, testAvatar.ID, res.ID)
				assert.Equal(t, testAvatar.UserID, res.UserID)
				assert.Equal(t, testAvatar.CreatedAt, res.CreatedAt)
				assert.Len(t, thumbs, 0)
			}
		})
	}
}

func TestGetMetadataList(t *testing.T) {
	ctx, repo := prepareDeps(t)
	testAvatars := avatarFixturesList(ctx, t, repo)
	tests := []struct {
		name          string
		filters       models.AvatarFilters
		correctAmount int
	}{
		{
			name: "success - with limit",
			filters: models.AvatarFilters{
				UserID: testAvatars[0].UserID,
				Limit:  5,
			},
			correctAmount: 5,
		},
		{
			name: "success - with limit & offset",
			filters: models.AvatarFilters{
				UserID: testAvatars[0].UserID,
				Limit:  7,
				Offset: 2,
			},
			correctAmount: 7,
		},
		{
			name: "success - empty response",
			filters: models.AvatarFilters{
				UserID: "2",
			},
			correctAmount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, thumbs, err := repo.GetMetadataList(ctx, tt.filters)
			if err != nil {
				t.Fatal(err)
			}
			assert.Len(t, res, tt.correctAmount)
			assert.Len(t, thumbs, 0)
			if tt.correctAmount > 0 {
				assert.Less(t, res[tt.correctAmount-1].CreatedAt, res[0].CreatedAt)
			}
		})
	}
}

func TestSaveThumbnail(t *testing.T) {
	ctx, repo := prepareDeps(t)
	testAvatar := avatarFixture(ctx, t, repo)
	tests := []struct {
		name                 string
		body                 models.SaveThumbnail
		failure              bool
		correctThumbnailsNum int
	}{
		{
			name: "success",
			body: models.SaveThumbnail{
				AvatarID:   testAvatar.ID,
				S3Key:      "test.png",
				Dimensions: "300x300",
			},
			failure:              false,
			correctThumbnailsNum: 1,
		},
		{
			name: "success",
			body: models.SaveThumbnail{
				AvatarID:   testAvatar.ID,
				S3Key:      "test.png",
				Dimensions: "100x100",
			},
			failure:              false,
			correctThumbnailsNum: 2,
		},
		{
			name: "failure - avatar does not exist",
			body: models.SaveThumbnail{
				AvatarID:   uuid.New(),
				S3Key:      "test.png",
				Dimensions: "300x300",
			},
			failure: true,
		},
		{
			name: "failure - invalid dimensions",
			body: models.SaveThumbnail{
				AvatarID:   uuid.New(),
				S3Key:      "test.png",
				Dimensions: "400x400",
			},
			failure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := repo.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = repo.SaveThumbnail(ctx, tx, tt.body)
			assert.Equal(t, tt.failure, err != nil)
			if !tt.failure {
				err = repo.CommitTx(tx)
				_, thumbnails, err := repo.GetMetadata(ctx, models.AvatarFilters{ID: tt.body.AvatarID})
				assert.NoError(t, err)
				assert.Len(t, thumbnails, tt.correctThumbnailsNum)
				if tt.correctThumbnailsNum == 1 {
					assert.Equal(t, thumbnails[0].Dimensions, tt.body.Dimensions)
					assert.Equal(t, thumbnails[0].S3Key, tt.body.S3Key)
				} else {
					assert.Equal(t, thumbnails[1].Dimensions, tt.body.Dimensions)
					assert.Equal(t, thumbnails[1].S3Key, tt.body.S3Key)
				}

			} else {
				repo.RollbackTx(tx)
			}
		})
	}
}

func TestUpdateStatus(t *testing.T) {
	ctx, repo := prepareDeps(t)
	testAvatar := avatarFixture(ctx, t, repo)
	tests := []struct {
		name   string
		status models.UpdateAvatarStatus
	}{
		{
			name:   "success",
			status: models.UpdateAvatarStatus{AvatarID: testAvatar.ID, UploadStatus: "uploaded", UpdatedAt: time.Now()},
		},
		{
			name:   "avatar not found",
			status: models.UpdateAvatarStatus{AvatarID: uuid.New(), UploadStatus: "uploaded", UpdatedAt: time.Now()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Println("test")
			tx, err := repo.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			err = repo.UpdateStatus(ctx, tx, tt.status)
			if err != nil {
				t.Fatal(err)
			}

			err = repo.CommitTx(tx)
			if err != nil {
				t.Fatal(err)
			}
			res, thumbs, err := repo.GetMetadata(ctx, models.AvatarFilters{ID: testAvatar.ID})
			assert.NoError(t, err)
			assert.Len(t, thumbs, 0)
			assert.NotNil(t, res.UpdatedAt)
		})
	}
}

func TestDeleteAvatarWithThumbnails(t *testing.T) {
	ctx, repo := prepareDeps(t)
	testAvatar := avatarFixture(ctx, t, repo)
	thumbnailFixtures(ctx, t, repo, testAvatar.ID)
	tests := []struct {
		name         string
		s3KeysNumber int
		filters      models.AvatarFilters
		failure      bool
	}{
		{
			name:         "success",
			s3KeysNumber: 3,
			filters: models.AvatarFilters{
				ID: testAvatar.ID,
			},
			failure: false,
		},
		{
			name:         "avatar not found",
			s3KeysNumber: 0,
			filters: models.AvatarFilters{
				ID: uuid.New(),
			},
			failure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := repo.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			s3Keys, err := repo.DeleteAvatarWithThumbnails(ctx, tx, tt.filters)
			if err != nil {
				assert.ErrorIs(t, err, ErrAvatarNotFound)
				repo.RollbackTx(tx)
				return
			}
			err = repo.CommitTx(tx)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.failure {
				assert.Equal(t, len(s3Keys), tt.s3KeysNumber)
				_, _, err = repo.GetMetadata(ctx, models.AvatarFilters{ID: tt.filters.ID})
				assert.ErrorIs(t, err, ErrAvatarNotFound)
			} else {
				repo.RollbackTx(tx)
			}
		})
	}
}
