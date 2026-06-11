package webhook

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/tight-line/sgotel/internal/publisher"
	"github.com/tight-line/sgotel/internal/sendgrid"
)

const (
	resultOK           = "ok"
	resultMethod       = "method_not_allowed"
	resultBadBody      = "bad_body"
	resultBadSignature = "bad_signature"
	resultBadPayload   = "bad_payload"
	resultQueueFull    = "queue_full"
	resultCancelled    = "canceled"
)

type Handler struct {
	Verifier  *sendgrid.Verifier
	Publisher *publisher.Publisher
	Recorder  publisher.RequestRecorder
	Logger    *slog.Logger
}

func New(v *sendgrid.Verifier, p *publisher.Publisher, r publisher.RequestRecorder, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if r == nil {
		r = publisher.NopRecorder{}
	}
	return &Handler{Verifier: v, Publisher: p, Recorder: r, Logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.Recorder.RecordRequest(ctx, resultMethod)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	// coverage:ignore - defensive; httptest readers don't error in unit tests
	if err != nil {
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

	for _, e := range events {
		if err := h.Publisher.Enqueue(ctx, e); err != nil {
			switch err.(type) {
			case publisher.ErrQueueFull:
				h.Recorder.RecordRequest(ctx, resultQueueFull)
				http.Error(w, "queue full", http.StatusServiceUnavailable)
			default: // coverage:ignore - defensive; only reachable when ctx is canceled mid-enqueue (shutdown race)
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
