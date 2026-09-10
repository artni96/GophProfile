package avatars

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

var ErrAvatarNotFound = errors.New("avatar not found")
var ErrNotOwner = errors.New("not owner")

type Repository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

func NewRepository(db *sqlx.DB, logger *zap.Logger) *Repository {
	return &Repository{
		db:     db,
		logger: logger,
	}
}

func (r *Repository) Save(ctx context.Context, avatar models.SaveAvatarRequest) (
	res models.SaveAvatarResponse, err error,
) {
	insertStmt := `
		INSERT INTO avatars (id, user_id, file_name, mime_type, size_bytes, s3_key, height, width, upload_status) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, user_id, upload_status, created_at;`
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		r.logger.Debug("failed to begin transaction", zap.Error(err))
		return res, fmt.Errorf("failed to begin transaction: %w", err)
	}
	err = tx.Get(
		&res,
		insertStmt,
		avatar.ID,
		avatar.UserID,
		avatar.FileName,
		avatar.MimeType,
		avatar.SizeBytes,
		avatar.S3Key,
		avatar.Height,
		avatar.Width,
		avatar.UploadStatus,
	)
	if err != nil {
		r.logger.Debug("failed to save avatar", zap.Error(err))
		return res, fmt.Errorf("failed to save avatar: %w", err)
	}

	if err = tx.Commit(); err != nil {
		r.logger.Debug("failed to commit transaction", zap.Error(err))
		return res, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return res, nil
}

func (r *Repository) GetMetadata(ctx context.Context, flt models.AvatarFilters) (
	res models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		r.logger.Debug("failed to begin transaction", zap.Error(err))
		return res, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	var stmt string
	if flt.ID != uuid.Nil() && flt.UserID == "" {
		stmt = `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars 
		WHERE id = $1 AND deleted_at IS NULL;`
		err = tx.Get(&res, stmt, flt.ID)
		if err != nil {
			r.logger.Debug("failed to get avatar by id", zap.String("id", flt.ID.String()), zap.Error(err))
			return res, nil, ErrAvatarNotFound
		}
	} else if flt.ID == uuid.Nil() && flt.UserID != "" {
		stmt = `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars 
		WHERE user_id = $1 AND deleted_at IS NULL 
		ORDER BY created_at DESC 
		LIMIT 1;`
		err = tx.Get(&res, stmt, flt.UserID)
		if err != nil {
			r.logger.Debug("failed to get last user avatar", zap.String("user_id", flt.UserID), zap.Error(err))
			return res, nil, ErrAvatarNotFound
		}
	}

	thumbnailsStmt := `
		SELECT avatar_id, dimensions, s3_key 
		FROM avatar_thumbnails 
		WHERE avatar_id = $1;`
	err = tx.Select(&thumbnails, thumbnailsStmt, res.ID)
	if err != nil {
		r.logger.Debug("failed to get thumbnails", zap.String("id", res.ID.String()), zap.Error(err))
		return res, nil, fmt.Errorf("failed to get thumbnails: %w", err)
	}
	if err = tx.Commit(); err != nil {
		r.logger.Debug("failed to commit transaction", zap.String("id", res.ID.String()), zap.Error(err))
		return res, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return res, thumbnails, nil
}

func (r *Repository) GetMetadataList(ctx context.Context, flt models.AvatarFilters) (
	originals []models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error) {
	originalStmt := `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 
		OFFSET $3;`
	err = r.db.SelectContext(ctx, &originals, originalStmt, flt.UserID, flt.Limit, flt.Offset)
	if err != nil {
		r.logger.Debug("failed to get avatars", zap.String("user_id", flt.UserID), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to get avatars: %w", err)
	}
	var originalsIDs []uuid.UUID
	for _, original := range originals {
		originalsIDs = append(originalsIDs, original.ID)
	}
	thumbnailsStmt := `
		SELECT avatar_id, dimensions, s3_key 
		FROM avatar_thumbnails 
		WHERE avatar_id = ANY($1);`
	err = r.db.SelectContext(ctx, &thumbnails, thumbnailsStmt, originalsIDs)
	if err != nil {
		r.logger.Debug("failed to get thumbnails", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to get thumbnails: %w", err)
	}
	return originals, thumbnails, nil

}

func (r *Repository) SaveThumbnail(ctx context.Context, t models.SaveThumbnail) error {
	stmt := `
			INSERT INTO avatar_thumbnails (avatar_id, s3_key,dimensions) 
			VALUES ($1, $2, $3);`
	_, err := r.db.ExecContext(ctx, stmt, t.AvatarID, t.S3Key, t.Dimensions)
	if err != nil {
		r.logger.Debug("failed to save thumbnail for avatar",
			zap.String("id", t.AvatarID.String()), zap.String("dimensions", t.Dimensions), zap.Error(err))
		return fmt.Errorf("failed to save thumbnail for avatar: %w", err)
	}
	return nil
}

type AvatarIDs3keyData struct {
	ID     uuid.UUID `db:"id"`
	S3Key  string    `db:"s3_key"`
	UserID string    `db:"user_id"`
}

func (r *Repository) DeleteAvatarWithThumbnails(ctx context.Context, flt models.AvatarFilters) (
	[]string, error) {
	s3Keys := make([]string, 0, 3)
	thumbnailsS3Keys := make([]string, 0, 2)
	var originalAvatarData AvatarIDs3keyData
	var originalStmt string
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		r.logger.Debug("failed to begin transaction", zap.Error(err))
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	if flt.ID != uuid.Nil() {
		originalStmt = `UPDATE avatars 
						SET deleted_at = NOW() 
						WHERE id = $1 RETURNING id, s3_key, user_id;`
		err = tx.Get(&originalAvatarData, originalStmt, flt.ID)
		if err != nil {
			r.logger.Debug("failed to update avatar by id", zap.String("id", flt.ID.String()), zap.Error(err))
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAvatarNotFound
			}
			return nil, err
		}
	} else if flt.ID == uuid.Nil() && flt.UserID != "" {
		originalStmt = `UPDATE avatars 
						SET deleted_at = NOW() 
						WHERE id = (
							SELECT id 
							FROM avatars 
							WHERE user_id = $1 
							ORDER BY created_at DESC 
						    LIMIT 1)
						RETURNING id, s3_key, user_id;`
		err = tx.Get(&originalAvatarData, originalStmt, flt.UserID)
		if err != nil {
			r.logger.Debug(
				"failed to update last user avatar", zap.String("id", flt.ID.String()), zap.Error(err))
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAvatarNotFound
			}
			return nil, err
		}
	}
	if originalAvatarData.UserID != flt.UserID && flt.UserID != "" && flt.ID != uuid.Nil() {
		err = tx.Rollback()
		if err != nil {
			r.logger.Debug("failed to rollback transaction", zap.String("id", flt.ID.String()), zap.Error(err))
			return nil, fmt.Errorf("failed to rollback transaction: %w", err)
		}
		r.logger.Debug(
			"failed to delete avatar: user not owner",
			zap.String("user_id", flt.UserID),
			zap.String("avatar_owner_id", originalAvatarData.UserID))
		return nil, ErrNotOwner
	}

	if originalAvatarData.S3Key != "" {
		s3Keys = append(s3Keys, originalAvatarData.S3Key)
	}
	thumbnailsStmt := `
		DELETE 
		FROM avatar_thumbnails 
		WHERE avatar_id = $1 RETURNING s3_key;`
	err = tx.Select(&thumbnailsS3Keys, thumbnailsStmt, originalAvatarData.ID.String())
	if err != nil {
		r.logger.Debug("failed to delete thumbnails", zap.String("id", originalAvatarData.ID.String()), zap.Error(err))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAvatarNotFound
		}
		return nil, err
	}
	if len(thumbnailsS3Keys) > 0 {
		s3Keys = append(s3Keys, thumbnailsS3Keys...)
	}
	if err = tx.Commit(); err != nil {
		r.logger.Debug("failed to commit transaction", zap.String("id", originalAvatarData.ID.String()), zap.Error(err))
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	r.logger.Debug("avatar with thumbnails successfully deleted")
	return s3Keys, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, status models.UpdateAvatarStatus) error {
	var stmt string
	var err error
	if status.ProcessingStatus != "" && status.UploadStatus == "" {
		stmt = `
			UPDATE avatars 
			SET processing_status = $1, updated_at = $2 
			WHERE id = $3;`
		_, err = r.db.ExecContext(ctx, stmt, status.ProcessingStatus, status.UpdatedAt, status.AvatarID)
	} else {
		stmt = `
			UPDATE avatars 
			SET upload_status = $1, updated_at = $2 
			WHERE id = $3;`
		_, err = r.db.ExecContext(ctx, stmt, status.UploadStatus, status.UpdatedAt, status.AvatarID)
	}
	if err != nil {
		r.logger.Debug(
			"failed to update avatar status",
			zap.String("id", status.AvatarID.String()),
			zap.String("upload_status", status.UploadStatus),
			zap.String("processing_status", status.ProcessingStatus),
			zap.Error(err),
		)
		return fmt.Errorf("failed to update avatar status: %w", err)
	}
	return nil
}
