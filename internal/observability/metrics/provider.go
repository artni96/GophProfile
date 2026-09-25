package metrics

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

type config struct {
	OtelCollectorHTTPInternalPort string `env:"OTEL_COLLECTOR_HTTP_INTERNAL_PORT"`
}

func InitMeterProvider(ctx context.Context) func() {
	cfg := &config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}
	exporter, err := otlpmetrichttp.New(
		ctx,
		otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithEndpoint(fmt.Sprintf("otel-collector:%s", cfg.OtelCollectorHTTPInternalPort)),
	)

	if err != nil {
		log.Fatalf("failed to create OTLP exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("gp-service"),
			semconv.ServiceVersionKey.String("1.0.0"),
			attribute.String("environment", os.Getenv("GO_ENV")),
		),
	)

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(
			metric.NewPeriodicReader(
				exporter,
				metric.WithInterval(1*time.Second),
			),
		),
	)
	otel.SetMeterProvider(meterProvider)

	return func() {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		if err := meterProvider.Shutdown(ctx); err != nil {
			otel.Handle(err)
		}
	}
}
