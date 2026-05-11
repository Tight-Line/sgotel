package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/tight-line/sgotel/internal/config"
	"github.com/tight-line/sgotel/internal/sendgrid"
)

// --- pure helpers -----------------------------------------------------------

func TestSeverityFor(t *testing.T) {
	cases := []struct {
		event string
		want  otellog.Severity
		text  string
	}{
		{"delivered", otellog.SeverityInfo, "INFO"},
		{"open", otellog.SeverityInfo, "INFO"},
		{"click", otellog.SeverityInfo, "INFO"},
		{"processed", otellog.SeverityInfo, "INFO"},
		{"deferred", otellog.SeverityWarn, "WARN"},
		{"bounce", otellog.SeverityError, "ERROR"},
		{"dropped", otellog.SeverityError, "ERROR"},
		{"spam_report", otellog.SeverityError, "ERROR"},
	}
	for _, c := range cases {
		gotSev, gotText := severityFor(c.event)
		if gotSev != c.want || gotText != c.text {
			t.Errorf("severityFor(%q) = (%v,%q), want (%v,%q)", c.event, gotSev, gotText, c.want, c.text)
		}
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[string]string{
		"":       "unknown",
		"250 OK": "2xx",
		"421":    "4xx",
		"5.1.1":  "5xx",
		"weird":  "unknown",
	}
	for in, want := range cases {
		if got := statusClass(in); got != want {
			t.Errorf("statusClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderEmail(t *testing.T) {
	const email = "Alice@Example.com"
	if got := renderEmail("", config.RedactNone); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := renderEmail(email, config.RedactNone); got != email {
		t.Errorf("none: got %q, want raw", got)
	}
	if got := renderEmail(email, config.RedactDrop); got != "" {
		t.Errorf("drop: got %q, want empty", got)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(email)))
	want := hex.EncodeToString(sum[:])
	if got := renderEmail(email, config.RedactHash); got != want {
		t.Errorf("hash: got %q, want %q", got, want)
	}
	// Case-insensitivity: same hash for different cases.
	if a, b := renderEmail("Foo@Bar.com", config.RedactHash), renderEmail("foo@bar.com", config.RedactHash); a != b {
		t.Errorf("hash not case-insensitive: %q vs %q", a, b)
	}
}

func TestLogBody(t *testing.T) {
	e := sendgrid.Event{Event: "delivered", Email: "a@b.com"}
	if got := logBody(e, config.RedactNone); got != "delivered a@b.com" {
		t.Errorf("got %q", got)
	}
	if got := logBody(e, config.RedactDrop); got != "delivered" {
		t.Errorf("drop got %q", got)
	}
	if got := logBody(sendgrid.Event{Event: "processed"}, config.RedactNone); got != "processed" {
		t.Errorf("no email got %q", got)
	}
}

func TestAnyToLogValue(t *testing.T) {
	if v := anyToLogValue("hi"); v.Kind() != otellog.KindString || v.AsString() != "hi" {
		t.Errorf("string: %+v", v)
	}
	if v := anyToLogValue(true); v.Kind() != otellog.KindBool || v.AsBool() != true {
		t.Errorf("bool: %+v", v)
	}
	if v := anyToLogValue(float64(42)); v.Kind() != otellog.KindFloat64 || v.AsFloat64() != 42 {
		t.Errorf("float: %+v", v)
	}
	if v := anyToLogValue(nil); v.Kind() != otellog.KindString || v.AsString() != "" {
		t.Errorf("nil: %+v", v)
	}
	if v := anyToLogValue([]any{"a", float64(1)}); v.Kind() != otellog.KindSlice {
		t.Errorf("slice kind: %v", v.Kind())
	} else if got := v.AsSlice(); len(got) != 2 {
		t.Errorf("slice len: %d", len(got))
	}
	if v := anyToLogValue(map[string]any{"k": "v"}); v.Kind() != otellog.KindMap {
		t.Errorf("map kind: %v", v.Kind())
	}
	// Unknown type falls back to a stringified representation.
	type customType struct{ X int }
	if v := anyToLogValue(customType{X: 7}); v.Kind() != otellog.KindString {
		t.Errorf("unknown type kind: %v", v.Kind())
	}
}

func TestStatusClass_2xxAndUnknown(t *testing.T) {
	if got := statusClass("250"); got != "2xx" {
		t.Errorf("2xx: %q", got)
	}
	if got := statusClass("999"); got != "unknown" {
		t.Errorf("unknown class: %q", got)
	}
}

func TestEmptyToUnknown(t *testing.T) {
	if got := emptyToUnknown(""); got != "unknown" {
		t.Errorf("empty: %q", got)
	}
	if got := emptyToUnknown("hard"); got != "hard" {
		t.Errorf("non-empty: %q", got)
	}
}

// --- Publisher behavior -----------------------------------------------------

type slowSink struct {
	count atomic.Int64
	hold  chan struct{}
}

func (s *slowSink) Publish(_ context.Context, _ sendgrid.Event) {
	if s.hold != nil {
		<-s.hold
	}
	s.count.Add(1)
}

func TestPublisher_EnqueueAndDrain(t *testing.T) {
	s := &slowSink{}
	p := New(s, 4, config.QueueFullBlock)
	p.Start(2)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := p.Enqueue(ctx, sendgrid.Event{Event: "delivered"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	sCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(sCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := s.count.Load(); got != 10 {
		t.Errorf("published: %d, want 10", got)
	}
}

func TestPublisher_ShedReturnsErrWhenFull(t *testing.T) {
	// No workers: the first enqueue fills the buffer deterministically, the
	// second must shed.
	p := New(&slowSink{}, 1, config.QueueFullShed)
	ctx := context.Background()
	if err := p.Enqueue(ctx, sendgrid.Event{Event: "a"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := p.Enqueue(ctx, sendgrid.Event{Event: "b"})
	if !errors.As(err, &ErrQueueFull{}) {
		t.Fatalf("second: got %v, want ErrQueueFull", err)
	}

	// Drain so Shutdown completes.
	p.Start(1)
	sCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Shutdown(sCtx)
}

func TestPublisher_BlockHonorsContextCancel(t *testing.T) {
	p := New(&slowSink{}, 1, config.QueueFullBlock)
	bg := context.Background()
	_ = p.Enqueue(bg, sendgrid.Event{Event: "a"}) // fills buffer

	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	err := p.Enqueue(ctx, sendgrid.Event{Event: "b"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}

	p.Start(1)
	sCtx, sCancel := context.WithTimeout(bg, 2*time.Second)
	defer sCancel()
	_ = p.Shutdown(sCtx)
}

func TestNopRecorder(t *testing.T) {
	// Exercise the no-op methods so they show as covered in the publisher package.
	r := NopRecorder{}
	r.RecordBatch(context.Background(), 1)
	r.RecordRequest(context.Background(), "ok")
}

func TestErrQueueFull_Error(t *testing.T) {
	if got := (ErrQueueFull{}).Error(); got == "" {
		t.Errorf("ErrQueueFull message should not be empty")
	}
}

func TestPublisher_ShutdownHonorsContextCancel(t *testing.T) {
	// Pin a worker on a sink that never returns so Shutdown can't drain
	// the queue; verify the canceled ctx makes Shutdown return its error.
	s := &slowSink{hold: make(chan struct{})}
	p := New(s, 1, config.QueueFullBlock)
	p.Start(1)
	_ = p.Enqueue(context.Background(), sendgrid.Event{Event: "a"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Shutdown(ctx); err == nil {
		t.Errorf("want shutdown to honor ctx deadline")
	}

	close(s.hold)
	finalCtx, finalCancel := context.WithTimeout(context.Background(), time.Second)
	defer finalCancel()
	_ = p.Shutdown(finalCtx)
}

func TestPublisher_ShutdownIdempotent(t *testing.T) {
	p := New(&slowSink{}, 4, config.QueueFullBlock)
	p.Start(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

// --- otelSink emit ----------------------------------------------------------

type emittedRecord struct {
	body       string
	severity   otellog.Severity
	eventName  string
	attributes map[string]otellog.Value
}

// recordingExporter is a sdklog.Exporter that captures every emitted record.
// Paired with a SimpleProcessor it gives us synchronous, in-memory log capture
// for tests without needing the sealed otellog.Logger interface.
type recordingExporter struct {
	mu      sync.Mutex
	records []emittedRecord
}

func (e *recordingExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range records {
		r := records[i]
		rec := emittedRecord{
			body:       r.Body().AsString(),
			severity:   r.Severity(),
			eventName:  r.EventName(),
			attributes: make(map[string]otellog.Value),
		}
		r.WalkAttributes(func(kv otellog.KeyValue) bool {
			rec.attributes[kv.Key] = kv.Value
			return true
		})
		e.records = append(e.records, rec)
	}
	return nil
}

func (e *recordingExporter) Shutdown(context.Context) error   { return nil }
func (e *recordingExporter) ForceFlush(context.Context) error { return nil }

func (e *recordingExporter) snapshot() []emittedRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]emittedRecord, len(e.records))
	copy(out, e.records)
	return out
}

func newTestSink(t *testing.T, redact config.RedactMode) (*otelSink, *recordingExporter, *sdkmetric.ManualReader) {
	t.Helper()
	exp := &recordingExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	sink, err := newOTelSink(lp.Logger("test"), mp.Meter("test"), redact)
	if err != nil {
		t.Fatalf("newOTelSink: %v", err)
	}
	t.Cleanup(func() {
		_ = lp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	return sink, exp, reader
}

func TestOTelSink_LogAttributes(t *testing.T) {
	sink, exp, _ := newTestSink(t, config.RedactNone)
	e := sendgrid.Event{
		Event:       "click",
		Email:       "alice@example.com",
		Timestamp:   1700000000,
		SGEventID:   "evt-1",
		SGMessageID: "msg-1",
		SMTPID:      "<smtp-1>",
		Category:    sendgrid.Categories{"welcome"},
		URL:         "https://example.com",
		UserAgent:   "Mozilla/5.0",
		IP:          "1.2.3.4",
		Response:    "250 OK",
		Attempt:     "3",
		Custom:      map[string]any{"campaign": "spring"},
	}
	sink.Publish(context.Background(), e)

	records := exp.snapshot()
	if len(records) != 1 {
		t.Fatalf("records: %d", len(records))
	}
	r := records[0]
	if r.severity != otellog.SeverityInfo {
		t.Errorf("severity: %v", r.severity)
	}
	if r.eventName != "sendgrid.click" {
		t.Errorf("eventName: %q", r.eventName)
	}
	if r.body != "click alice@example.com" {
		t.Errorf("body: %q", r.body)
	}
	want := map[string]string{
		"sendgrid.event":           "click",
		"sendgrid.event_id":        "evt-1",
		"sendgrid.message_id":      "msg-1",
		"sendgrid.smtp_id":         "<smtp-1>",
		"sendgrid.email":           "alice@example.com",
		"sendgrid.url":             "https://example.com",
		"sendgrid.useragent":       "Mozilla/5.0",
		"sendgrid.ip":              "1.2.3.4",
		"sendgrid.response":        "250 OK",
		"sendgrid.attempt":         "3",
		"sendgrid.custom.campaign": "spring",
	}
	for k, v := range want {
		got, ok := r.attributes[k]
		if !ok {
			t.Errorf("missing attribute %q", k)
			continue
		}
		if got.AsString() != v {
			t.Errorf("attr %q: got %q, want %q", k, got.AsString(), v)
		}
	}
	cat, ok := r.attributes["sendgrid.category"]
	if !ok || cat.Kind() != otellog.KindSlice {
		t.Errorf("category attr: %+v ok=%v", cat, ok)
	} else if got := cat.AsSlice(); len(got) != 1 || got[0].AsString() != "welcome" {
		t.Errorf("category values: %+v", got)
	}
}

func TestOTelSink_RedactHashEmail(t *testing.T) {
	sink, exp, _ := newTestSink(t, config.RedactHash)
	sink.Publish(context.Background(), sendgrid.Event{Event: "delivered", Email: "alice@example.com"})
	r := exp.snapshot()[0]
	got := r.attributes["sendgrid.email"].AsString()
	sum := sha256.Sum256([]byte("alice@example.com"))
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("email attr not hashed: %q", got)
	}
}

func TestOTelSink_RedactDropEmail(t *testing.T) {
	sink, exp, _ := newTestSink(t, config.RedactDrop)
	sink.Publish(context.Background(), sendgrid.Event{Event: "delivered", Email: "alice@example.com"})
	r := exp.snapshot()[0]
	if _, present := r.attributes["sendgrid.email"]; present {
		t.Errorf("email attr should be dropped")
	}
	if r.body != "delivered" {
		t.Errorf("body should omit email: %q", r.body)
	}
}

func TestOTelSink_BounceSeverityAndStatusClass(t *testing.T) {
	sink, exp, reader := newTestSink(t, config.RedactNone)
	sink.Publish(context.Background(), sendgrid.Event{
		Event:  "bounce",
		Email:  "x@y.com",
		Type:   "hard",
		Status: "5.1.1",
		Reason: "550 unknown user",
	})
	if exp.snapshot()[0].severity != otellog.SeverityError {
		t.Errorf("severity: %v", exp.snapshot()[0].severity)
	}

	bounces := findCounter(t, reader, "sendgrid.bounces.total")
	if len(bounces) != 1 || bounces[0].Value != 1 {
		t.Fatalf("bounces data points: %+v", bounces)
	}
	attrs := dpAttrs(bounces[0])
	if attrs["type"] != "hard" || attrs["status_class"] != "5xx" {
		t.Errorf("bounce attrs: %+v", attrs)
	}
}

func TestOTelSink_EventsCounter(t *testing.T) {
	sink, _, reader := newTestSink(t, config.RedactNone)
	sink.Publish(context.Background(), sendgrid.Event{Event: "delivered", Category: sendgrid.Categories{"welcome"}})
	sink.Publish(context.Background(), sendgrid.Event{Event: "delivered", Category: sendgrid.Categories{"welcome"}})
	sink.Publish(context.Background(), sendgrid.Event{Event: "open"})

	dps := findCounter(t, reader, "sendgrid.events.total")
	if len(dps) != 2 {
		t.Fatalf("want 2 series (delivered/welcome and open/none), got %d: %+v", len(dps), dps)
	}
	got := map[string]int64{}
	for _, dp := range dps {
		a := dpAttrs(dp)
		got[a["event"]+"/"+a["category"]] = dp.Value
	}
	if got["delivered/welcome"] != 2 || got["open/none"] != 1 {
		t.Errorf("counts: %+v", got)
	}
}

func TestOTelSink_RecordBatchAndRequest(t *testing.T) {
	sink, _, reader := newTestSink(t, config.RedactNone)
	ctx := context.Background()
	sink.RecordBatch(ctx, 5)
	sink.RecordRequest(ctx, "ok")
	sink.RecordRequest(ctx, "bad_signature")
	sink.RecordRequest(ctx, "ok")

	reqs := findCounter(t, reader, "sendgrid.webhook.requests.total")
	counts := map[string]int64{}
	for _, dp := range reqs {
		counts[dpAttrs(dp)["result"]] = dp.Value
	}
	if counts["ok"] != 2 || counts["bad_signature"] != 1 {
		t.Errorf("request counts: %+v", counts)
	}

	rm := metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !hasMetric(rm, "sendgrid.webhook.batch.size") {
		t.Errorf("batch.size histogram not emitted")
	}
}

// --- metric collection helpers ---------------------------------------------

func findCounter(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	rm := metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s: data is %T, want Sum[int64]", name, m.Data)
			}
			return sum.DataPoints
		}
	}
	t.Fatalf("metric %q not found", name)
	return nil
}

func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}

func dpAttrs(dp metricdata.DataPoint[int64]) map[string]string {
	out := map[string]string{}
	iter := dp.Attributes.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		out[string(kv.Key)] = kv.Value.AsString()
	}
	return out
}
