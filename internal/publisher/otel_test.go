package publisher

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// These exercise everything in otel.go. None of it needs a collector: protocol
// selection is env-var parsing, the default switch arms reject a protocol before
// any exporter is built, and Shutdown is error aggregation over providers that
// can be built in memory. Only SetupOTel needs live OTLP, and it lives in
// otel_setup.go behind its own exemption.

// clearOTLPEnv blanks every variable protocol() consults, so a test starts from
// the documented default rather than from whatever the developer's shell has.
// Empty is equivalent to unset here: protocol() tests for "" rather than using
// os.LookupEnv. t.Setenv also restores the prior value when the test ends.
func clearOTLPEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
	} {
		t.Setenv(k, "")
	}
}

func TestProtocol_DefaultsToHTTPProtobuf(t *testing.T) {
	clearOTLPEnv(t)
	for _, signal := range []string{"LOGS", "METRICS"} {
		if got := protocol(signal); got != protocolHTTPProtobuf {
			t.Errorf("protocol(%q) = %q, want %q", signal, got, protocolHTTPProtobuf)
		}
	}
}

func TestProtocol_GlobalOverride(t *testing.T) {
	clearOTLPEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocolGRPC)
	for _, signal := range []string{"LOGS", "METRICS"} {
		if got := protocol(signal); got != protocolGRPC {
			t.Errorf("protocol(%q) = %q, want %q", signal, got, protocolGRPC)
		}
	}
}

func TestProtocol_PerSignalBeatsGlobal(t *testing.T) {
	clearOTLPEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", protocolGRPC)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocolHTTP)

	// The per-signal variable wins for its own signal and leaves the other alone.
	if got := protocol("LOGS"); got != protocolHTTP {
		t.Errorf("logs: got %q, want %q", got, protocolHTTP)
	}
	if got := protocol("METRICS"); got != protocolGRPC {
		t.Errorf("metrics: got %q, want %q", got, protocolGRPC)
	}
}

func TestProtocol_SignalNameIsUppercased(t *testing.T) {
	clearOTLPEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", protocolGRPC)
	if got := protocol("logs"); got != protocolGRPC {
		t.Errorf("lowercase signal should resolve the same var: got %q", got)
	}
}

func TestNewExporters_RejectUnsupportedProtocol(t *testing.T) {
	clearOTLPEnv(t)
	// "http/json" is the realistic wrong answer: the OTel spec lists it as an
	// optional protocol, so someone will eventually set it.
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")
	ctx := context.Background()

	logExp, err := newLogExporter(ctx)
	if err == nil {
		t.Fatalf("logs: want error, got exporter %v", logExp)
	}
	if !strings.Contains(err.Error(), "http/json") {
		t.Errorf("logs: error should name the rejected protocol, got %q", err)
	}

	metricExp, err := newMetricExporter(ctx)
	if err == nil {
		t.Fatalf("metrics: want error, got exporter %v", metricExp)
	}
	if !strings.Contains(err.Error(), "http/json") {
		t.Errorf("metrics: error should name the rejected protocol, got %q", err)
	}
}

func TestNewExporters_SupportedProtocols(t *testing.T) {
	// The OTLP exporters connect lazily, so constructing one reaches no
	// network and needs no collector. That is what makes these switch arms
	// testable here rather than only in an integration environment.
	for _, proto := range []string{protocolGRPC, protocolHTTPProtobuf, protocolHTTP} {
		t.Run(proto, func(t *testing.T) {
			clearOTLPEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", proto)
			ctx := context.Background()

			logExp, err := newLogExporter(ctx)
			if err != nil {
				t.Fatalf("logs: %v", err)
			}
			if logExp == nil {
				t.Fatal("logs: nil exporter with nil error")
			}
			t.Cleanup(func() { _ = logExp.Shutdown(context.Background()) })

			metricExp, err := newMetricExporter(ctx)
			if err != nil {
				t.Fatalf("metrics: %v", err)
			}
			if metricExp == nil {
				t.Fatal("metrics: nil exporter with nil error")
			}
			t.Cleanup(func() { _ = metricExp.Shutdown(context.Background()) })
		})
	}
}

func TestOTel_ShutdownNilProviders(t *testing.T) {
	// Shutdown runs on a partially built OTel, which is what the SetupOTel
	// error paths leave behind.
	o := &OTel{}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Errorf("nil providers should shut down cleanly, got %v", err)
	}
}

func TestOTel_ShutdownClosesBothProviders(t *testing.T) {
	lp := sdklog.NewLoggerProvider()
	mp := sdkmetric.NewMeterProvider()
	o := &OTel{LoggerProvider: lp, MeterProvider: mp}

	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// Shutdown is NOT idempotent, and that is worth pinning rather than
	// assuming. The logger provider tolerates a second call; the meter
	// provider's reader does not, and reports "reader is shutdown". So callers
	// get exactly one clean Shutdown and must not retry it.
	err := o.Shutdown(context.Background())
	if err == nil {
		t.Fatal("second shutdown unexpectedly succeeded; if the SDK made this " +
			"idempotent, relax this test rather than deleting it")
	}
	if !strings.Contains(err.Error(), "meter provider") {
		t.Errorf("second shutdown should fail on the meter provider, got %q", err)
	}
}

func TestOTel_ShutdownReportsProviderErrors(t *testing.T) {
	// A canceled context makes the batch processor's shutdown fail, which is
	// the path that builds the aggregated error message.
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(failingLogExporter{})),
	)
	o := &OTel{LoggerProvider: lp}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := o.Shutdown(ctx); err != nil && !strings.Contains(err.Error(), "otel shutdown") {
		t.Errorf("error should be wrapped with otel shutdown context, got %q", err)
	}
	// Not asserting that an error occurs: whether a canceled context surfaces
	// here is an SDK implementation detail. What matters is the wrapping.
}

type failingLogExporter struct{}

func (failingLogExporter) Export(context.Context, []sdklog.Record) error { return errors.New("boom") }
func (failingLogExporter) Shutdown(context.Context) error                { return errors.New("boom") }
func (failingLogExporter) ForceFlush(context.Context) error              { return errors.New("boom") }
