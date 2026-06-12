// coverage:ignore-file - binary entrypoint, exercised by integration not unit tests
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/tight-line/sgotel/internal/config"
	"github.com/tight-line/sgotel/internal/publisher"
	"github.com/tight-line/sgotel/internal/sendgrid"
	"github.com/tight-line/sgotel/internal/webhook"
)

// version is set at link time via -ldflags "-X main.version=...". The Dockerfile
// takes an ARG VERSION (default "dev") and the release/pr-images workflows pass
// the git tag or PR identifier. Logged on startup for operator visibility.
var version = "dev"

// coverage:ignore - binary entrypoint, exercised by integration not unit tests
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	logger.Info("starting sgotel", "version", version)

	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}

// coverage:ignore - binary entrypoint, exercised by integration not unit tests
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	verifier, err := sendgrid.NewVerifier(cfg.PublicKey, cfg.SignatureMaxAge)
	if err != nil {
		return err
	}

	ot, err := publisher.SetupOTel(ctx, cfg)
	if err != nil {
		return err
	}

	pub := publisher.New(ot.Sink, cfg.QueueSize, cfg.QueueFullBehavior)
	pub.Start(runtime.GOMAXPROCS(0))

	handler := webhook.New(verifier, pub, ot.Recorder, logger, cfg.MaxBodyBytes, cfg.EnqueueTimeout)

	mux := http.NewServeMux()
	mux.Handle(cfg.WebhookPath, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// WriteTimeout must outlast a block-mode enqueue wait so the 503 can still
	// be written when the queue is saturated; keep a margin above EnqueueTimeout.
	writeTimeout := 15 * time.Second
	if cfg.EnqueueTimeout > 0 && cfg.EnqueueTimeout+10*time.Second > writeTimeout {
		writeTimeout = cfg.EnqueueTimeout + 10*time.Second
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "webhook", cfg.WebhookPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "err", err.Error())
	}
	if err := pub.Shutdown(shutdownCtx); err != nil {
		logger.Error("publisher shutdown", "err", err.Error())
	}
	if err := ot.Shutdown(shutdownCtx); err != nil {
		logger.Error("otel shutdown", "err", err.Error())
	}
	return nil
}
