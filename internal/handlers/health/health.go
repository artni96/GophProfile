package health

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	_ "github.com/artni96/GophProfile/api/swagger"
	"github.com/artni96/GophProfile/internal/handlers/middlewares"
	"github.com/artni96/GophProfile/internal/models"
	"github.com/artni96/GophProfile/internal/services/avatars"
	"github.com/artni96/GophProfile/internal/storage"
	"github.com/artni96/GophProfile/pkg/interfaces"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	db       *sqlx.DB
	s3Client interfaces.S3I
	logger   *slog.Logger
	service  *avatars.Service
	tracer   trace.Tracer
}

func NewHealthHandler(
	db *sqlx.DB,
	s3Client *storage.S3Client,
	service *avatars.Service,
	logger *slog.Logger,
	tracer trace.Tracer,
) *Handler {
	return &Handler{
		db:       db,
		s3Client: s3Client,
		logger:   logger,
		service:  service,
		tracer:   tracer,
	}
}

// Check godoc
//
//	@Summary		App services health check
//	@Description	Check status of minio, database and rabbitmq.
//	@Tags			health
//	@Accept			json
//	@Produce		json
//	@Success		200	Each	of	the	services	works	correctly
//	@Failure		500
//	@Failure		503	{object}	models.HealthResponse
//	@Router			/health [get]
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	rootSpan := trace.SpanFromContext(r.Context())
	rootSpan.AddEvent("health_check.started")

	ctx, dbSpan := h.tracer.Start(r.Context(), "db.health_check")
	defer dbSpan.End()
	var res models.HealthResponse
	if err := h.db.PingContext(ctx); err != nil {
		h.logger.ErrorContext(ctx, "failed to ping database", "error", err)
		res.Database = false
		dbSpan.SetAttributes(attribute.Bool("db.is_healthy", false))
		dbSpan.SetAttributes(attribute.String("db.health_check_error", err.Error()))
		dbSpan.SetStatus(codes.Error, err.Error())
	} else {
		res.Database = true
		dbSpan.SetAttributes(attribute.Bool("db.is_healthy", true))
		dbSpan.SetStatus(codes.Unset, "db is healthy")
	}
	rootSpan.SetAttributes(attribute.Bool("db.is_healthy", res.Database))
	_, err := h.s3Client.Check(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to check S3 health", "error", err)
		res.S3 = false
		rootSpan.SetStatus(codes.Error, err.Error())
	} else {
		res.S3 = true
	}
	rootSpan.SetAttributes(attribute.Bool("s3.is_healthy", res.S3))
	err = h.service.Broker.Check(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to check broker health", "error", err)
		res.Broker = false
		rootSpan.SetStatus(codes.Error, err.Error())
	} else {
		res.Broker = true
	}
	rootSpan.SetAttributes(attribute.Bool("broker.is_healthy", res.Broker))
	rootSpan.AddEvent("health_check.completed")
	w.Header().Set("Content-Type", "application/json")
	bytesRes, err := json.Marshal(res)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to marshal health response", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !res.Broker || !res.Database || !res.S3 {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	w.Write(bytesRes)
}

func HealthRouter(
	db *sqlx.DB, s3Client *storage.S3Client,
	service *avatars.Service,
	logger *slog.Logger,
) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(otelhttp.NewMiddleware("health"))
	r.Use(middleware.Logger)
	r.Use(middlewares.PanicRecoverer(logger))
	r.Use(middlewares.GzipMiddleware)

	handler := NewHealthHandler(db, s3Client, service, logger, otel.Tracer("healthHandler"))

	r.Route("/", func(r chi.Router) {
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, "Method %s is forbidden", r.Method)
		})
		r.Get("/", handler.Check)
	})
	return r
}
