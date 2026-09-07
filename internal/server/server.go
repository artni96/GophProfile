package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/artni96/GophProfile/internal/config"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type HTTPServer struct {
	cfg    *config.Config
	logger *zap.Logger
	r      *chi.Mux
	server *http.Server
}

// InitHTTPServer initializes a new HTTP server.
func (s *HTTPServer) InitHTTPServer() {
	s.server = &http.Server{
		Addr:    s.cfg.ServerAddr,
		Handler: s.r,
	}
}

// Run launches the HTTP server.
func (s *HTTPServer) Run() error {
	s.InitHTTPServer()

	err := s.server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("failed to start HTTP server", zap.Error(err))
		return err
	}
	return nil
}

// Shutdown stops the active HTTP server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}
	return nil
}

func NewHTTPServer(cfg *config.Config, logger *zap.Logger, r *chi.Mux) *HTTPServer {
	return &HTTPServer{
		cfg:    cfg,
		logger: logger,
		r:      r,
	}
}
