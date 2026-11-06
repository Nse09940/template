package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const protocolHTTP = "http/protobuf"

// Telemetry owns the providers and instruments injected into the application.
type Telemetry struct {
	Logger         *slog.Logger
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	created        metric.Int64Counter
	completed      metric.Int64Counter
}

func New(ctx context.Context, logLevel string) (*Telemetry, error) {
	logger, err := NewLogger(os.Stdout, logLevel)
	if err != nil {
		return nil, err
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Error("telemetry export failed", "error", err)
	}))
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			attribute.String("service.name", "trip-service"),
			attribute.String("service.namespace", "tripgo"),
			attribute.String("service.version", version),
			attribute.String("deployment.environment", "local"),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	if protocol == "" {
		protocol = protocolHTTP
	}
	traceExporter, err := newTraceExporter(ctx, protocol)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	metricExporter, err := newMetricExporter(ctx, protocol)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create metric exporter: %w", err), traceExporter.Shutdown(ctx))
	}

	// Leaving the sampler unset makes the SDK honor OTEL_TRACES_SAMPLER and its ARG.
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithView(durationView("http.server.request.duration")),
		sdkmetric.WithView(durationView("db.client.operation.duration")),
	)
	telemetry := &Telemetry{
		Logger: logger, TracerProvider: tracerProvider, MeterProvider: meterProvider,
	}
	meter := meterProvider.Meter("trip-service")
	telemetry.created, err = meter.Int64Counter("tripgo.trips.created", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create trips-created counter: %w", err), telemetry.Shutdown(ctx))
	}
	telemetry.completed, err = meter.Int64Counter("tripgo.trips.completed", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create trips-completed counter: %w", err), telemetry.Shutdown(ctx))
	}
	return telemetry, nil
}

func (t *Telemetry) Created(ctx context.Context) {
	t.created.Add(ctx, 1)
}

func (t *Telemetry) Completed(ctx context.Context) {
	t.completed.Add(ctx, 1)
}

func (t *Telemetry) Shutdown(ctx context.Context) error {
	return errors.Join(t.MeterProvider.Shutdown(ctx), t.TracerProvider.Shutdown(ctx))
}

func newTraceExporter(ctx context.Context, protocol string) (sdktrace.SpanExporter, error) {
	switch protocol {
	case protocolHTTP:
		return otlptracehttp.New(ctx)
	case "grpc":
		return otlptracegrpc.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q", protocol)
	}
}

func newMetricExporter(ctx context.Context, protocol string) (sdkmetric.Exporter, error) {
	switch protocol {
	case protocolHTTP:
		return otlpmetrichttp.New(ctx)
	case "grpc":
		return otlpmetricgrpc.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q", protocol)
	}
}

func durationView(name string) sdkmetric.View {
	return sdkmetric.NewView(sdkmetric.Instrument{Name: name}, sdkmetric.Stream{
		Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
			Boundaries: []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 10},
		},
	})
}
