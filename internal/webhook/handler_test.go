package webhook

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tight-line/sgotel/internal/config"
	"github.com/tight-line/sgotel/internal/publisher"
	"github.com/tight-line/sgotel/internal/sendgrid"
)

type recordedSink struct {
	mu     sync.Mutex
	events []sendgrid.Event
}

func (s *recordedSink) Publish(_ context.Context, e sendgrid.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordedSink) get() []sendgrid.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sendgrid.Event, len(s.events))
	copy(out, s.events)
	return out
}

type recordedRecorder struct {
	mu       sync.Mutex
	batches  []int
	requests []string
}

func (r *recordedRecorder) RecordBatch(_ context.Context, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, n)
}

func (r *recordedRecorder) RecordRequest(_ context.Context, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, result)
}

func (r *recordedRecorder) lastRequest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return ""
	}
	return r.requests[len(r.requests)-1]
}

type fixture struct {
	priv     *ecdsa.PrivateKey
	verifier *sendgrid.Verifier
	sink     *recordedSink
	rec      *recordedRecorder
	pub      *publisher.Publisher
	handler  *Handler
}

func newFixture(t *testing.T, fullPolicy config.QueueFullBehavior, queueSize, workers int) *fixture {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pubkey: %v", err)
	}
	v, err := sendgrid.NewVerifier(base64.StdEncoding.EncodeToString(pubDER), 0)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	sink := &recordedSink{}
	rec := &recordedRecorder{}
	pub := publisher.New(sink, queueSize, fullPolicy)
	if workers > 0 {
		pub.Start(workers)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if workers == 0 {
			// Drain any queued events so Shutdown can return.
			pub.Start(1)
		}
		_ = pub.Shutdown(ctx)
	})
	h := New(v, pub, rec, nil)
	return &fixture{priv: priv, verifier: v, sink: sink, rec: rec, pub: pub, handler: h}
}

func (f *fixture) sign(t *testing.T, body []byte) (ts, sig string) {
	t.Helper()
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	h := sha256.New()
	h.Write([]byte(ts))
	h.Write(body)
	raw, err := ecdsa.SignASN1(rand.Reader, f.priv, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return ts, base64.StdEncoding.EncodeToString(raw)
}

func (f *fixture) post(t *testing.T, body []byte, ts, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set(sendgrid.TimestampHeader, ts)
	req.Header.Set(sendgrid.SignatureHeader, sig)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestHandler_HappyPath(t *testing.T) {
	// Single worker keeps order deterministic for assertion below.
	f := newFixture(t, config.QueueFullBlock, 16, 1)
	body := []byte(`[
		{"event":"delivered","email":"a@b.com","sg_event_id":"e1","sg_message_id":"m1","timestamp":1700000000},
		{"event":"open","email":"a@b.com","sg_event_id":"e2","sg_message_id":"m1","timestamp":1700000060}
	]`)
	ts, sig := f.sign(t, body)
	rr := f.post(t, body, ts, sig)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%q", rr.Code, rr.Body.String())
	}
	waitFor(t, func() bool { return len(f.sink.get()) == 2 })
	got := f.sink.get()
	if got[0].Event != "delivered" || got[1].Event != "open" {
		t.Errorf("events: %+v", got)
	}
	if f.rec.lastRequest() != resultOK {
		t.Errorf("last request: %q", f.rec.lastRequest())
	}
	if len(f.rec.batches) != 1 || f.rec.batches[0] != 2 {
		t.Errorf("batches: %v", f.rec.batches)
	}
}

func TestHandler_BadSignature(t *testing.T) {
	f := newFixture(t, config.QueueFullBlock, 16, 1)
	body := []byte(`[{"event":"delivered"}]`)
	rr := f.post(t, body, "1700000000", base64.StdEncoding.EncodeToString([]byte("bogus")))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := f.rec.lastRequest(); got != resultBadSignature {
		t.Errorf("result: %q", got)
	}
	if len(f.sink.get()) != 0 {
		t.Errorf("sink should be empty, got %v", f.sink.get())
	}
}

func TestHandler_BadPayload(t *testing.T) {
	f := newFixture(t, config.QueueFullBlock, 16, 1)
	body := []byte(`{"not":"an array"}`)
	ts, sig := f.sign(t, body)
	rr := f.post(t, body, ts, sig)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := f.rec.lastRequest(); got != resultBadPayload {
		t.Errorf("result: %q", got)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	f := newFixture(t, config.QueueFullBlock, 16, 1)
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestHandler_QueueFullShed(t *testing.T) {
	// No workers: the queue fills on the first event, second event sheds.
	f := newFixture(t, config.QueueFullShed, 1, 0)
	body := []byte(`[{"event":"delivered"},{"event":"open"}]`)
	ts, sig := f.sign(t, body)
	rr := f.post(t, body, ts, sig)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d body=%q", rr.Code, rr.Body.String())
	}
	if got := f.rec.lastRequest(); got != resultQueueFull {
		t.Errorf("result: %q", got)
	}
}
