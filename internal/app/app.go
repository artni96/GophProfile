package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/artni96/GophProfile/internal/config"
	"github.com/artni96/GophProfile/internal/handlers/avatars"
	"github.com/artni96/GophProfile/internal/handlers/health"
	avatarsrepo "github.com/artni96/GophProfile/internal/repository/avatars"
	"github.com/artni96/GophProfile/internal/server"
	avatarsserv "github.com/artni96/GophProfile/internal/services/avatars"
	"github.com/artni96/GophProfile/internal/storage"
	"github.com/artni96/GophProfile/internal/worker"
	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/github"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type App struct {
	Eg       *errgroup.Group
	Cfg      *config.Config
	Logger   *zap.Logger
	DB       *sqlx.DB
	S3Client *storage.S3Client
	Service  *avatarsserv.Service

	IsClosed bool

	Server *server.HTTPServer
	router *chi.Mux

	Broker *worker.Broker
}

// InitDBConn initializes a database connection according to the app config.
func (a *App) InitDBConn(ctx context.Context) error {
	if a.Cfg.DBDsn == "" {
		return fmt.Errorf("database dsn is not provided")
	}

	db, err := sqlx.Open("pgx", a.Cfg.DBDsn)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	localCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	err = db.PingContext(localCtx)
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	a.DB = db
	a.Logger.Info("database connection initialized successfully")
	return nil
}

// applyMigrations updates the atabase up to the latest migration file.
func (a *App) applyMigrations() error {
	driver, err := postgres.WithInstance(a.DB.DB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to initialize postgres driver: %w", err)
	}

	migrator, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize migrator: %w", err)
	}

	if err = migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	a.Logger.Info("migrations applied successfully")
	return nil
}

func (a *App) initRouter(ctx context.Context) error {
	a.router = chi.NewRouter()

	avatarRouter := avatars.AvatarRouter(ctx, a.Service)
	a.router.Mount("/api/v1/avatars", avatarRouter)

	healthRouter := health.HealthRouter(ctx, a.DB, a.S3Client, a.Logger)
	a.router.Mount("/api/v1/health", healthRouter)

	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get work dir: %w", err)
	}
	webDir := http.Dir(filepath.Join(workDir, "web/static"))
	a.router.Handle("/web/upload", http.StripPrefix("/web/upload", http.FileServer(webDir)))
	return nil
}

func (a *App) InitServer(ctx context.Context) error {
	err := a.initRouter(ctx)
	if err != nil {
		return err
	}
	a.Server = server.NewHTTPServer(a.Cfg, a.Logger, a.router)
	return nil
}

func (a *App) InitDependencies() {
	repo := avatarsrepo.NewRepository(a.DB, a.Logger)
	service := avatarsserv.NewService(repo, a.Logger, a.S3Client, a.Broker)
	a.Service = service
}

func (a *App) LaunchServer() {
	a.Eg.Go(func() error {
		return a.Server.Run()
	})
}

func (a *App) Shutdown(ctx context.Context, gsCancel context.CancelFunc) {
	a.Eg.Go(func() error {
		err := a.Server.Shutdown(ctx)
		if err != nil {
			a.Logger.Error("failed to shutdown http server", zap.Error(err))
			return fmt.Errorf("failed to shutdown http server: %w", err)
		}
		err = a.DB.Close()
		if err != nil {
			a.Logger.Error("failed to close database", zap.Error(err))
			return fmt.Errorf("failed to close database: %w", err)
		}
		gsCancel()
		a.IsClosed = true
		return nil
	})
}

func (a *App) initS3Client(cfg *config.Config, logger *zap.Logger) error {
	s3client, err := storage.NewS3Client(cfg, logger, a.Cfg.S3.BucketName)
	if err != nil {
		return err
	}
	a.S3Client = s3client
	return nil
}

func (a *App) initBroker(logger *zap.Logger) error {
	broker, err := worker.NewBroker(logger)
	if err != nil {
		return err
	}
	a.Broker = broker
	return nil
}

func NewApp(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*App, error) {
	app := &App{
		Eg:     new(errgroup.Group),
		Cfg:    cfg,
		Logger: logger,
	}
	err := app.InitDBConn(ctx)
	if err != nil {
		app.Logger.Fatal("failed to initialize database connection", zap.Error(err))
	}
	err = app.initS3Client(cfg, app.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize s3 client: %w", err)
	}
	err = app.initBroker(app.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize broker: %w", err)
	}
	app.InitDependencies()
	err = app.InitServer(ctx)
	if err != nil {
		app.Logger.Fatal("failed to initialize server", zap.Error(err))
		return nil, fmt.Errorf("failed to initialize server: %w", err)
	}

	return app, nil
}
