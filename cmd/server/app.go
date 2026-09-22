package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/artni96/GophProfile/internal/app"
	"github.com/artni96/GophProfile/internal/config"
	"github.com/artni96/GophProfile/internal/logger"
	"github.com/artni96/GophProfile/internal/worker"
)

func run(cfg *config.Config) error {
	ctx := context.Background()
	gfPeriod := time.Second * 5
	gfCtx, gfCancel := context.WithTimeout(ctx, gfPeriod)
	defer gfCancel()

	appLogger, otelShutdown := logger.InitLogger(ctx)
	defer otelShutdown()

	app, err := app.NewApp(ctx, cfg, appLogger)
	if err != nil {
		app.Logger.Info("failed to init app", "error", err)
		return fmt.Errorf("failed to init app")
	}
	app.LaunchServer()

	wp := worker.NewPool(app.Broker, app.Eg, app.Service, app.Logger)
	go wp.Launch(ctx)

	shutdownCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT)
	defer stop()

	<-shutdownCtx.Done()

	app.Logger.Info("shutting the app down")
	app.Shutdown(gfCtx, gfCancel)
	if err != nil {
		return err
	}
	select {
	case <-gfCtx.Done():
		if !app.IsClosed {
			app.Logger.Info("grace period has been expired - app closed forcefully")
			os.Exit(0)
		}
		app.Logger.Info("app closed gracefully")
	}
	return nil
}
