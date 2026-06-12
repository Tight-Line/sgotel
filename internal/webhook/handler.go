package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tight-line/sgotel/internal/publisher"
	"github.com/tight-line/sgotel/internal/sendgrid"
)

// defaultMaxBodyBytes bounds the request body when a Handler is built without
// an explicit limit. Keeps the handler safe-by-default even if a caller passes
// a non-positive value.
const defaultMaxBodyBytes int64 = 5 << 20 // 5 MiB

const (
	resultOK             = "ok"
	resultMethod         = "method_not_allowed"
	resultBadBody        = "bad_body"
	resultBodyTooLarge   = "body_too_large"
	resultBadSignature   = "bad_signature"
	resultBadPayload     = "bad_payload"
	resultQueueFull      = "queue_full"
	resultEnqueueTimeout = "enqueue_timeout"
	resultCancelled      = "canceled"
)

type Handler struct {
	Verifier  *sendgrid.Verifier
	Publisher *publisher.Publisher
	Recorder  publisher.RequestRecorder
	Logger    *slog.Logger

	// MaxBodyBytes caps the request body read before signature verification.
	MaxBodyBytes int64
	// EnqueueTimeout bounds how long a request waits for queue space in block
	// mode before shedding with a 503. Zero waits indefinitely.
	EnqueueTimeout time.Duration
}

func New(v *sendgrid.Verifier, p *publisher.Publisher, r publisher.RequestRecorder, logger *slog.Logger, maxBodyBytes int64, enqueueTimeout time.Duration) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if r == nil {
		r = publisher.NopRecorder{}
	}
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	return &Handler{
		Verifier:       v,
		Publisher:      p,
		Recorder:       r,
		Logger:         logger,
		MaxBodyBytes:   maxBodyBytes,
		EnqueueTimeout: enqueueTimeout,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.Recorder.RecordRequest(ctx, resultMethod)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cap the body before reading so an oversized POST can't exhaust memory
	// ahead of (and independent of) signature verification.
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.Recorder.RecordRequest(ctx, resultBodyTooLarge)
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		// coverage:ignore - defensive; non-size read errors don't occur with httptest readers
		h.Recorder.RecordRequest(ctx, resultBadBody)
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get(sendgrid.SignatureHeader)
	ts := r.Header.Get(sendgrid.TimestampHeader)
	if err := h.Verifier.Verify(ts, sig, body); err != nil {
		h.Recorder.RecordRequest(ctx, resultBadSignature)
		h.Logger.Warn("signature rejected", "err", err.Error())
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	events, err := sendgrid.ParseBatch(body)
	if err != nil {
		h.Recorder.RecordRequest(ctx, resultBadPayload)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	h.Recorder.RecordBatch(ctx, len(events))

	// Bound the whole-batch enqueue to one deadline so a stalled publisher sheds
	// to a 503 (which SendGrid retries) instead of parking the goroutine. A
	// single deadline across the loop avoids giving a large batch N× the budget.
	enqCtx := ctx
	if h.EnqueueTimeout > 0 {
		var cancel context.CancelFunc
		enqCtx, cancel = context.WithTimeout(ctx, h.EnqueueTimeout)
		defer cancel()
	}

	for _, e := range events {
		if err := h.Publisher.Enqueue(enqCtx, e); err != nil {
			switch {
			case errors.As(err, &publisher.ErrQueueFull{}):
				h.Recorder.RecordRequest(ctx, resultQueueFull)
				http.Error(w, "queue full", http.StatusServiceUnavailable)
			case errors.Is(err, context.DeadlineExceeded):
				h.Recorder.RecordRequest(ctx, resultEnqueueTimeout)
				w.Header().Set("Retry-After", "5")
				http.Error(w, "queue full, retry", http.StatusServiceUnavailable)
			default: // coverage:ignore - defensive; only reachable when the request ctx is canceled mid-enqueue (shutdown race)
				h.Recorder.RecordRequest(ctx, resultCancelled)
				http.Error(w, "shutting down", http.StatusServiceUnavailable)
			}
			return
		}
	}

	h.Logger.Info("webhook batch accepted", "events", len(events))
	h.Recorder.RecordRequest(ctx, resultOK)
	w.WriteHeader(http.StatusOK)
}
