package health

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/artni96/GophProfile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

type Handler struct {
	ctx      context.Context
	db       *sqlx.DB
	s3Client *storage.S3Client
	logger   *zap.Logger
}

func NewHealthHandler(ctx context.Context, db *sqlx.DB, s3Client *storage.S3Client, logger *zap.Logger) *Handler {
	return &Handler{
		ctx:      ctx,
		db:       db,
		s3Client: s3Client,
		logger:   logger,
	}
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var errs []string
	if err := h.db.PingContext(h.ctx); err != nil {
		h.logger.Error("failed to ping database", zap.Error(err))
		errs = append(errs, "failed to ping database")
	}
	_, err := h.s3Client.Check()
	if err != nil {
		h.logger.Error("failed to check S3 health", zap.Error(err))
		errs = append(errs, "failed to check S3 health")
	}
	if len(errs) > 0 {
		w.WriteHeader(http.StatusInternalServerError)
		resp := fmt.Sprintf("%s", strings.Join(errs, "; "))
		w.Write([]byte(fmt.Sprintf("error: %s", resp)))
		return
	}
	w.WriteHeader(http.StatusOK)
}

func HealthRouter(ctx context.Context, db *sqlx.DB, s3Client *storage.S3Client, logger *zap.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	handler := NewHealthHandler(ctx, db, s3Client, logger)

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Method %s is forbidden", r.Method)))
		})
		r.Get("/", handler.Check)
	})
	return r
}
