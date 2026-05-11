package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	"github.com/tight-line/sgotel/internal/config"
	"github.com/tight-line/sgotel/internal/sendgrid"
)

// Sink consumes parsed SendGrid events. Implementations are responsible for
// emitting OTel logs and metrics (or, in tests, recording calls).
type Sink interface {
	Publish(ctx context.Context, e sendgrid.Event)
}

// RequestRecorder is called by the webhook handler to record per-request
// metrics that aren't tied to a single event (batch size, request outcome).
type RequestRecorder interface {
	RecordBatch(ctx context.Context, n int)
	RecordRequest(ctx context.Context, result string)
}

// NopRecorder is a RequestRecorder that drops all calls. Useful in tests.
type NopRecorder struct{}

func (NopRecorder) RecordBatch(context.Context, int)      {}
func (NopRecorder) RecordRequest(context.Context, string) {}

// Publisher buffers events and fans them out to a Sink via a worker pool.
// Webhook handlers should call Enqueue; the publisher decouples webhook ack
// latency from OTLP export latency.
type Publisher struct {
	sink       Sink
	queue      chan sendgrid.Event
	fullPolicy config.QueueFullBehavior

	wg       sync.WaitGroup
	closeOne sync.Once
}

func New(sink Sink, queueSize int, fullPolicy config.QueueFullBehavior) *Publisher {
	return &Publisher{
		sink:       sink,
		queue:      make(chan sendgrid.Event, queueSize),
		fullPolicy: fullPolicy,
	}
}

// Start launches n worker goroutines. Call Shutdown to drain and stop them.
func (p *Publisher) Start(workers int) {
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.run()
	}
}

func (p *Publisher) run() {
	defer p.wg.Done()
	for e := range p.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		p.sink.Publish(ctx, e)
		cancel()
	}
}

// ErrQueueFull is returned by Enqueue when shed mode is active and the queue is full.
type ErrQueueFull struct{}

func (ErrQueueFull) Error() string { return "publisher queue full" }

// Enqueue submits an event. In block mode it waits until space is available
// (or ctx is canceled). In shed mode it returns ErrQueueFull immediately.
func (p *Publisher) Enqueue(ctx context.Context, e sendgrid.Event) error {
	switch p.fullPolicy {
	case config.QueueFullShed:
		select {
		case p.queue <- e:
			return nil
		default:
			return ErrQueueFull{}
		}
	default: // block
		select {
		case p.queue <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Shutdown closes the queue and waits for workers to drain in-flight events.
// Safe to call multiple times; subsequent calls just wait for workers.
func (p *Publisher) Shutdown(ctx context.Context) error {
	p.closeOne.Do(func() { close(p.queue) })
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// otelSink is the production Sink that writes OTel logs + metrics.
type otelSink struct {
	logger otellog.Logger
	now    func() time.Time

	eventsTotal   metric.Int64Counter
	bouncesTotal  metric.Int64Counter
	batchSize     metric.Int64Histogram
	requestsTotal metric.Int64Counter

	redact config.RedactMode
}

func newOTelSink(logger otellog.Logger, meter metric.Meter, redact config.RedactMode) (*otelSink, error) {
	events, err := meter.Int64Counter("sendgrid.events.total",
		metric.WithDescription("Count of SendGrid webhook events by type."))
	// coverage:ignore - defensive; instrument creation only fails on name conflict
	if err != nil {
		return nil, fmt.Errorf("counter sendgrid.events.total: %w", err)
	}
	bounces, err := meter.Int64Counter("sendgrid.bounces.total",
		metric.WithDescription("Count of SendGrid bounce events by type and SMTP status class."))
	// coverage:ignore - defensive; instrument creation only fails on name conflict
	if err != nil {
		return nil, fmt.Errorf("counter sendgrid.bounces.total: %w", err)
	}
	batches, err := meter.Int64Histogram("sendgrid.webhook.batch.size",
		metric.WithDescription("Number of events per inbound SendGrid webhook batch."))
	// coverage:ignore - defensive; instrument creation only fails on name conflict
	if err != nil {
		return nil, fmt.Errorf("histogram sendgrid.webhook.batch.size: %w", err)
	}
	requests, err := meter.Int64Counter("sendgrid.webhook.requests.total",
		metric.WithDescription("Count of inbound webhook POSTs by outcome."))
	// coverage:ignore - defensive; instrument creation only fails on name conflict
	if err != nil {
		return nil, fmt.Errorf("counter sendgrid.webhook.requests.total: %w", err)
	}
	return &otelSink{
		logger:        logger,
		now:           time.Now,
		eventsTotal:   events,
		bouncesTotal:  bounces,
		batchSize:     batches,
		requestsTotal: requests,
		redact:        redact,
	}, nil
}

// RecordBatch is called by the webhook handler to report per-request metrics.
func (s *otelSink) RecordBatch(ctx context.Context, n int) {
	s.batchSize.Record(ctx, int64(n))
}

// RecordRequest is called by the webhook handler to count request outcomes.
func (s *otelSink) RecordRequest(ctx context.Context, result string) {
	s.requestsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

func (s *otelSink) Publish(ctx context.Context, e sendgrid.Event) {
	s.emitLog(ctx, e)
	s.emitMetrics(ctx, e)
}

func (s *otelSink) emitLog(ctx context.Context, e sendgrid.Event) {
	var r otellog.Record
	if e.Timestamp > 0 {
		r.SetTimestamp(time.Unix(e.Timestamp, 0))
	} else {
		r.SetTimestamp(s.now())
	}
	r.SetObservedTimestamp(s.now())
	sev, sevText := severityFor(e.Event)
	r.SetSeverity(sev)
	r.SetSeverityText(sevText)
	r.SetEventName("sendgrid." + e.Event)
	r.SetBody(otellog.StringValue(logBody(e, s.redact)))

	attrs := make([]otellog.KeyValue, 0, 16)
	attrs = append(attrs,
		otellog.String("sendgrid.event", e.Event),
		otellog.String("sendgrid.message_id", e.SGMessageID),
		otellog.String("sendgrid.event_id", e.SGEventID),
	)
	if e.SMTPID != "" {
		attrs = append(attrs, otellog.String("sendgrid.smtp_id", e.SMTPID))
	}
	if email := renderEmail(e.Email, s.redact); email != "" {
		attrs = append(attrs, otellog.String("sendgrid.email", email))
	}
	if len(e.Category) > 0 {
		vals := make([]otellog.Value, len(e.Category))
		for i, c := range e.Category {
			vals[i] = otellog.StringValue(c)
		}
		attrs = append(attrs, otellog.KeyValue{
			Key:   "sendgrid.category",
			Value: otellog.SliceValue(vals...),
		})
	}
	if e.Reason != "" {
		attrs = append(attrs, otellog.String("sendgrid.bounce.reason", e.Reason))
	}
	if e.Status != "" {
		attrs = append(attrs, otellog.String("sendgrid.bounce.status", e.Status))
	}
	if e.Type != "" {
		attrs = append(attrs, otellog.String("sendgrid.bounce.type", e.Type))
	}
	if e.URL != "" {
		attrs = append(attrs, otellog.String("sendgrid.url", e.URL))
	}
	if e.UserAgent != "" {
		attrs = append(attrs, otellog.String("sendgrid.useragent", e.UserAgent))
	}
	if e.IP != "" {
		attrs = append(attrs, otellog.String("sendgrid.ip", e.IP))
	}
	if e.Response != "" {
		attrs = append(attrs, otellog.String("sendgrid.response", e.Response))
	}
	if e.Attempt != "" {
		attrs = append(attrs, otellog.String("sendgrid.attempt", e.Attempt))
	}
	for k, v := range e.Custom {
		attrs = append(attrs, otellog.KeyValue{
			Key:   "sendgrid.custom." + k,
			Value: anyToLogValue(v),
		})
	}
	r.AddAttributes(attrs...)

	s.logger.Emit(ctx, r)
}

func (s *otelSink) emitMetrics(ctx context.Context, e sendgrid.Event) {
	category := "none"
	if len(e.Category) > 0 {
		category = e.Category[0]
	}
	s.eventsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("event", e.Event),
		attribute.String("category", category),
	))
	if e.Event == "bounce" {
		s.bouncesTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("type", emptyToUnknown(e.Type)),
			attribute.String("status_class", statusClass(e.Status)),
		))
	}
}

func severityFor(event string) (sev otellog.Severity, text string) {
	switch event {
	case "bounce", "dropped", "spam_report":
		return otellog.SeverityError, "ERROR"
	case "deferred":
		return otellog.SeverityWarn, "WARN"
	default:
		return otellog.SeverityInfo, "INFO"
	}
}

func logBody(e sendgrid.Event, redact config.RedactMode) string {
	email := renderEmail(e.Email, redact)
	if email == "" {
		return e.Event
	}
	return e.Event + " " + email
}

func renderEmail(email string, mode config.RedactMode) string {
	if email == "" {
		return ""
	}
	switch mode {
	case config.RedactDrop:
		return ""
	case config.RedactHash:
		sum := sha256.Sum256([]byte(strings.ToLower(email)))
		return hex.EncodeToString(sum[:])
	default:
		return email
	}
}

func statusClass(status string) string {
	if status == "" {
		return "unknown"
	}
	switch status[0] {
	case '2':
		return "2xx"
	case '4':
		return "4xx"
	case '5':
		return "5xx"
	default:
		return "unknown"
	}
}

func emptyToUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func anyToLogValue(v any) otellog.Value {
	switch t := v.(type) {
	case string:
		return otellog.StringValue(t)
	case bool:
		return otellog.BoolValue(t)
	case float64:
		// JSON numbers decode to float64. Preserve as float; downstream can
		// coerce to int if needed.
		return otellog.Float64Value(t)
	case []any:
		vals := make([]otellog.Value, len(t))
		for i, x := range t {
			vals[i] = anyToLogValue(x)
		}
		return otellog.SliceValue(vals...)
	case map[string]any:
		kvs := make([]otellog.KeyValue, 0, len(t))
		for k, x := range t {
			kvs = append(kvs, otellog.KeyValue{Key: k, Value: anyToLogValue(x)})
		}
		return otellog.MapValue(kvs...)
	case nil:
		return otellog.StringValue("")
	default:
		return otellog.StringValue(fmt.Sprintf("%v", t))
	}
}
