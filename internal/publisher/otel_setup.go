// coverage:ignore-file - constructs live OTLP exporters and SDK providers;
// exercised against a real collector, not unit tests.
//
// SetupOTel lives in its own file purely so that exemption covers it alone.
// The rest of otel.go is env-var parsing and error aggregation that unit tests
// reach without a collector, and it is tested.

package publisher

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/tight-line/sgotel/internal/config"
)

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

	var logger = lp.Logger("github.com/tight-line/sgotel")
	var meter = mp.Meter("github.com/tight-line/sgotel")

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
