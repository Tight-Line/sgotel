package publisher

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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

// OTLP protocol values accepted by OTEL_EXPORTER_OTLP_PROTOCOL. "http" is
// accepted as an alias for "http/protobuf"; SGOtel does not support
// "http/json", which the OTel spec lists as optional.
const (
	protocolHTTPProtobuf = "http/protobuf"
	protocolHTTP         = "http"
	protocolGRPC         = "grpc"
)

func protocol(signal string) string {
	if v := os.Getenv("OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal) + "_PROTOCOL"); v != "" {
		return v
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); v != "" {
		return v
	}
	return protocolHTTPProtobuf
}

func newLogExporter(ctx context.Context) (log.Exporter, error) {
	switch protocol("LOGS") {
	case protocolGRPC:
		return otlploggrpc.New(ctx)
	case protocolHTTPProtobuf, protocolHTTP:
		return otlploghttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP logs protocol: %q", protocol("LOGS"))
	}
}

func newMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	switch protocol("METRICS") {
	case protocolGRPC:
		return otlpmetricgrpc.New(ctx)
	case protocolHTTPProtobuf, protocolHTTP:
		return otlpmetrichttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol: %q", protocol("METRICS"))
	}
}
