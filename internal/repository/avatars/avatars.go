package avatars

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var ErrAvatarNotFound = errors.New("avatar not found")
var ErrNotOwner = errors.New("not owner")

type Repository struct {
	db     *sqlx.DB
	tracer trace.Tracer
}

func NewRepository(db *sqlx.DB, tracer trace.Tracer) *Repository {
	return &Repository{
		db:     db,
		tracer: tracer,
	}
}

func (r *Repository) BeginTx(ctx context.Context) (*sqlx.Tx, error) {
	return r.db.BeginTxx(ctx, nil)
}

func (r *Repository) CommitTx(tx *sqlx.Tx) error {
	return tx.Commit()
}

func (r *Repository) RollbackTx(tx *sqlx.Tx) error {
	return tx.Rollback()
}

func (r *Repository) Save(ctx context.Context, avatar models.SaveAvatarRequest) (
	res models.SaveAvatarResponse, err error,
) {
	ctx, span := r.tracer.Start(ctx, "avatars.Repo.Save")
	defer span.End()

	span.SetAttributes(
		attribute.String("id", avatar.ID.String()),
		attribute.String("user_id", avatar.UserID),
		attribute.String("file_name", avatar.FileName),
		attribute.String("mime_type", avatar.MimeType),
		attribute.Int64("size_bytes", int64(avatar.SizeBytes)),
		attribute.Int64("height", int64(avatar.Height)),
		attribute.Int64("width", int64(avatar.Width)),
	)
	insertStmt := `
		INSERT INTO avatars (id, user_id, file_name, mime_type, size_bytes, s3_key, height, width, upload_status) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, user_id, upload_status, created_at;`
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, fmt.Errorf("failed to save avatar: %w", err)
	}

	if err = tx.Commit(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		err = tx.Rollback()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return res, fmt.Errorf("failed to rollback transaction after failure: %w", err)
		}
		return res, fmt.Errorf("failed to commit transaction: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return res, nil
}

func (r *Repository) GetMetadata(ctx context.Context, flt models.AvatarFilters) (
	res models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error) {
	ctx, span := r.tracer.Start(ctx, "avatars.Repo.GetMetadata")
	defer span.End()

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return res, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	var stmt string
	if flt.ID != uuid.Nil() {
		stmt = `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars 
		WHERE id = $1 AND deleted_at IS NULL;`
		err = tx.Get(&res, stmt, flt.ID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			err = tx.Rollback()
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return models.AvatarDBEntity{}, nil, err
			}
			return res, nil, ErrAvatarNotFound
		}
		if flt.UserID != "" && flt.UserID != res.UserID {
			err = tx.Rollback()
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return models.AvatarDBEntity{}, nil, err
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, ErrNotOwner.Error())
			return res, nil, ErrNotOwner
		}
	} else if flt.UserID != "" {
		stmt = `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars 
		WHERE user_id = $1 AND deleted_at IS NULL 
		ORDER BY created_at DESC 
		LIMIT 1;`
		err = tx.Get(&res, stmt, flt.UserID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			err = tx.Rollback()
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return models.AvatarDBEntity{}, nil, err
			}
			return res, nil, ErrAvatarNotFound
		}
	}

	thumbnailsStmt := `
		SELECT avatar_id, dimensions, s3_key 
		FROM avatar_thumbnails 
		WHERE avatar_id = $1;`
	err = tx.Select(&thumbnails, thumbnailsStmt, res.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		err = tx.Rollback()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return models.AvatarDBEntity{}, nil, err
		}
		return res, nil, fmt.Errorf("failed to get thumbnails: %w", err)
	}
	if err = tx.Commit(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		err = tx.Rollback()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return models.AvatarDBEntity{}, nil, err
		}
		return res, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return res, thumbnails, nil
}

func (r *Repository) GetMetadataList(ctx context.Context, flt models.AvatarFilters) (
	originals []models.AvatarDBEntity, thumbnails []models.GetThumbnailMetadata, err error) {
	ctx, span := r.tracer.Start(ctx, "avatars.Repo.GetMetadataList")
	defer span.End()

	originalStmt := `
		SELECT id, user_id, file_name, mime_type, size_bytes, height, width, created_at, updated_at, s3_key 
		FROM avatars
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 
		OFFSET $3;`
	err = r.db.SelectContext(ctx, &originals, originalStmt, flt.UserID, flt.Limit, flt.Offset)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, nil, fmt.Errorf("failed to get thumbnails: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return originals, thumbnails, nil

}

func (r *Repository) SaveThumbnail(ctx context.Context, tx *sqlx.Tx, t models.SaveThumbnail) error {
	ctx, span := r.tracer.Start(ctx, "avatars.repo.SaveThumbnail")
	defer span.End()
	stmt := `
			INSERT INTO avatar_thumbnails (avatar_id, s3_key,dimensions) 
			VALUES ($1, $2, $3);`
	_, err := tx.ExecContext(ctx, stmt, t.AvatarID, t.S3Key, t.Dimensions)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		err = r.RollbackTx(tx)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("failed to rollback transaction after failure: %w", err)
		}
		return fmt.Errorf("failed to save thumbnail for avatar: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

type AvatarIDs3keyData struct {
	ID     uuid.UUID `db:"id"`
	S3Key  string    `db:"s3_key"`
	UserID string    `db:"user_id"`
}

func (r *Repository) DeleteAvatarWithThumbnails(ctx context.Context, tx *sqlx.Tx, flt models.AvatarFilters) (
	[]string, error) {
	ctx, span := r.tracer.Start(ctx, "avatars.repo.DeleteAvatarWithThumbnails")
	defer span.End()

	s3Keys := make([]string, 0, 3)
	thumbnailsS3Keys := make([]string, 0, 2)
	var originalAvatarData AvatarIDs3keyData
	var originalStmt string
	if flt.ID != uuid.Nil() {
		span.SetAttributes(attribute.String("avatar_id", flt.ID.String()))
		originalStmt = `UPDATE avatars 
						SET deleted_at = NOW() 
						WHERE id = $1 RETURNING id, s3_key, user_id;`
		err := tx.Get(&originalAvatarData, originalStmt, flt.ID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAvatarNotFound
			}
			return nil, err
		}
	} else if flt.ID == uuid.Nil() && flt.UserID != "" {
		span.SetAttributes(attribute.String("user_id", flt.UserID))
		originalStmt = `UPDATE avatars 
						SET deleted_at = NOW() 
						WHERE id = (
							SELECT id 
							FROM avatars 
							WHERE user_id = $1 
							ORDER BY created_at DESC 
						    LIMIT 1)
						RETURNING id, s3_key, user_id;`
		err := tx.Get(&originalAvatarData, originalStmt, flt.UserID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAvatarNotFound
			}
			return nil, err
		}
	}
	if originalAvatarData.UserID != flt.UserID && flt.UserID != "" && flt.ID != uuid.Nil() {
		err := tx.Rollback()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("failed to rollback transaction: %w", err)
		}
		return nil, ErrNotOwner
	}

	if originalAvatarData.S3Key != "" {
		s3Keys = append(s3Keys, originalAvatarData.S3Key)
	}
	thumbnailsStmt := `
		DELETE 
		FROM avatar_thumbnails 
		WHERE avatar_id = $1 RETURNING s3_key;`
	err := tx.Select(&thumbnailsS3Keys, thumbnailsStmt, originalAvatarData.ID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAvatarNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if len(thumbnailsS3Keys) > 0 {
		s3Keys = append(s3Keys, thumbnailsS3Keys...)
	}
	return s3Keys, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, tx *sqlx.Tx, status models.UpdateAvatarStatus) error {
	ctx, span := r.tracer.Start(ctx, "avatars.repo.UpdateStatus")
	defer span.End()

	var stmt string
	var err error
	if status.ProcessingStatus != "" && status.UploadStatus == "" {
		stmt = `
			UPDATE avatars 
			SET processing_status = $1, updated_at = $2 
			WHERE id = $3;`
		_, err = tx.ExecContext(ctx, stmt, status.ProcessingStatus, status.UpdatedAt, status.AvatarID)
	} else {
		stmt = `
			UPDATE avatars 
			SET upload_status = $1, updated_at = $2 
			WHERE id = $3;`
		_, err = tx.ExecContext(ctx, stmt, status.UploadStatus, status.UpdatedAt, status.AvatarID)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to update avatar status: %w", err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}
