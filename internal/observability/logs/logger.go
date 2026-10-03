package logger

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func InitLogger(ctx context.Context) (*slog.Logger, func(), error) {
	exporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP exporter for logs: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("gp-service"),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create log resource: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)
	handler := otelslog.NewHandler(
		"gp-service",
		otelslog.WithLoggerProvider(loggerProvider),
	)
	baseLogger := slog.New(handler)
	logger := baseLogger.With("service", "gp-service")
	slog.SetDefault(logger)

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := loggerProvider.Shutdown(ctx); err != nil {
			otel.Handle(err)
		}
	}
	return logger, shutdown, nil
}
