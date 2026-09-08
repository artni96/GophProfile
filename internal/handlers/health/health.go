package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/internal/services/avatars"
	"github.com/artni96/GophProfile/internal/storage"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
)

type Handler struct {
	ctx      context.Context
	db       *sqlx.DB
	s3Client interfaces.S3I
	logger   *zap.Logger
	service  *avatars.Service
}

func NewHealthHandler(ctx context.Context, db *sqlx.DB, s3Client *storage.S3Client, service *avatars.Service, logger *zap.Logger) *Handler {
	return &Handler{
		ctx:      ctx,
		db:       db,
		s3Client: s3Client,
		logger:   logger,
		service:  service,
	}
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var res models.HealthResponse
	if err := h.db.PingContext(h.ctx); err != nil {
		h.logger.Error("failed to ping database", zap.Error(err))
		res.Database = false
	} else {
		res.Database = true
	}
	_, err := h.s3Client.Check()
	if err != nil {
		h.logger.Error("failed to check S3 health", zap.Error(err))
		res.S3 = false
	} else {
		res.S3 = true
	}
	err = h.service.Broker.Check()
	if err != nil {
		h.logger.Error("failed to check broker health", zap.Error(err))
		res.Broker = false
	} else {
		res.Broker = true
	}
	w.Header().Set("Content-Type", "application/json")
	if !res.Broker || !res.Database || !res.S3 {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	bytesRes, err := json.Marshal(res)
	if err != nil {
		h.logger.Error("failed to marshal health response", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(bytesRes)
}

func HealthRouter(ctx context.Context, db *sqlx.DB, s3Client *storage.S3Client, service *avatars.Service, logger *zap.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	handler := NewHealthHandler(ctx, db, s3Client, service, logger)

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Method %s is forbidden", r.Method)))
		})
		r.Get("/", handler.Check)
	})
	return r
}
