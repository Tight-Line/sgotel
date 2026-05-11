package publisher

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/tight-line/sgotel/internal/config"
)

// OTel groups the providers built by SetupOTel so callers can shut them down
// in the reverse order they were created.
type OTel struct {
	Sink           Sink
	Recorder       RequestRecorder
	LoggerProvider *log.LoggerProvider
	MeterProvider  *sdkmetric.MeterProvider
}

// Shutdown flushes and closes the providers. Honors the supplied context.
func (o *OTel) Shutdown(ctx context.Context) error {
	var errs []error
	if o.LoggerProvider != nil {
		if err := o.LoggerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logger provider: %w", err))
		}
	}
	if o.MeterProvider != nil {
		if err := o.MeterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider: %w", err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("otel shutdown: %v", errs)
}

// SetupOTel constructs the OTLP-backed log + metric pipelines and returns a
// Sink that writes to them. Protocol selection honors OTEL_EXPORTER_OTLP_PROTOCOL
// (and per-signal overrides). Endpoint and other knobs come from the standard
// OTel env vars, handled by the exporter constructors.
func SetupOTel(ctx context.Context, cfg *config.Config) (*OTel, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			// service.name identifies the relay; messaging.system facets all
			// signals as "from the SendGrid pipeline" so backends can group on it.
			attribute.String("messaging.system", "sendgrid"),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	logExp, err := newLogExporter(ctx)
	if err != nil {
		return nil, err
	}
	lp := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExp)),
	)

	metricExp, err := newMetricExporter(ctx)
	if err != nil {
		_ = lp.Shutdown(ctx)
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
	)

	var logger otellog.Logger = lp.Logger("github.com/tight-line/sgotel")
	var meter metric.Meter = mp.Meter("github.com/tight-line/sgotel")

	sink, err := newOTelSink(logger, meter, cfg.RedactEmail)
	if err != nil {
		_ = lp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		return nil, err
	}
	return &OTel{
		Sink:           sink,
		Recorder:       sink,
		LoggerProvider: lp,
		MeterProvider:  mp,
	}, nil
}

func protocol(signal string) string {
	if v := os.Getenv("OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal) + "_PROTOCOL"); v != "" {
		return v
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); v != "" {
		return v
	}
	return "http/protobuf"
}

func newLogExporter(ctx context.Context) (log.Exporter, error) {
	switch protocol("LOGS") {
	case "grpc":
		return otlploggrpc.New(ctx)
	case "http/protobuf", "http":
		return otlploghttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP logs protocol: %q", protocol("LOGS"))
	}
}

func newMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	switch protocol("METRICS") {
	case "grpc":
		return otlpmetricgrpc.New(ctx)
	case "http/protobuf", "http":
		return otlpmetrichttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol: %q", protocol("METRICS"))
	}
}
