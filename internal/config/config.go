package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type RedactMode string

const (
	RedactNone RedactMode = "none"
	RedactHash RedactMode = "hash"
	RedactDrop RedactMode = "drop"
)

type QueueFullBehavior string

const (
	QueueFullBlock QueueFullBehavior = "block"
	QueueFullShed  QueueFullBehavior = "shed"
)

type Config struct {
	ListenAddr        string
	WebhookPath       string
	PublicKey         string
	SignatureMaxAge   time.Duration
	RedactEmail       RedactMode
	QueueSize         int
	QueueFullBehavior QueueFullBehavior
	ServiceName       string
}

func Load() (*Config, error) {
	c := &Config{
		ListenAddr:  getenv("SGOTEL_LISTEN_ADDR", ":8080"),
		WebhookPath: getenv("SGOTEL_WEBHOOK_PATH", "/webhook"),
		PublicKey:   os.Getenv("SGOTEL_SENDGRID_PUBLIC_KEY"),
		ServiceName: getenv("OTEL_SERVICE_NAME", "sgotel"),
	}
	if c.PublicKey == "" {
		return nil, errors.New("SGOTEL_SENDGRID_PUBLIC_KEY is required")
	}

	maxAge, err := time.ParseDuration(getenv("SGOTEL_SIGNATURE_MAX_AGE", "5m"))
	if err != nil {
		return nil, fmt.Errorf("SGOTEL_SIGNATURE_MAX_AGE: %w", err)
	}
	c.SignatureMaxAge = maxAge

	qs, err := strconv.Atoi(getenv("SGOTEL_QUEUE_SIZE", "1024"))
	if err != nil {
		return nil, fmt.Errorf("SGOTEL_QUEUE_SIZE: %w", err)
	}
	if qs < 1 {
		return nil, errors.New("SGOTEL_QUEUE_SIZE must be >= 1")
	}
	c.QueueSize = qs

	switch RedactMode(getenv("SGOTEL_REDACT_EMAIL", "none")) {
	case RedactNone:
		c.RedactEmail = RedactNone
	case RedactHash:
		c.RedactEmail = RedactHash
	case RedactDrop:
		c.RedactEmail = RedactDrop
	default:
		return nil, errors.New("SGOTEL_REDACT_EMAIL must be one of: none, hash, drop")
	}

	switch QueueFullBehavior(getenv("SGOTEL_QUEUE_FULL_BEHAVIOR", "block")) {
	case QueueFullBlock:
		c.QueueFullBehavior = QueueFullBlock
	case QueueFullShed:
		c.QueueFullBehavior = QueueFullShed
	default:
		return nil, errors.New("SGOTEL_QUEUE_FULL_BEHAVIOR must be one of: block, shed")
	}

	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
