package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("SGOTEL_SENDGRID_PUBLIC_KEY", "dummy")
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr: %q", c.ListenAddr)
	}
	if c.WebhookPath != "/webhook" {
		t.Errorf("WebhookPath: %q", c.WebhookPath)
	}
	if c.SignatureMaxAge != 5*time.Minute {
		t.Errorf("SignatureMaxAge: %v", c.SignatureMaxAge)
	}
	if c.QueueSize != 1024 {
		t.Errorf("QueueSize: %d", c.QueueSize)
	}
	if c.RedactEmail != RedactNone {
		t.Errorf("RedactEmail: %q", c.RedactEmail)
	}
	if c.QueueFullBehavior != QueueFullBlock {
		t.Errorf("QueueFullBehavior: %q", c.QueueFullBehavior)
	}
	if c.ServiceName != "sgotel" {
		t.Errorf("ServiceName: %q", c.ServiceName)
	}
}

func TestLoad_MissingPublicKey(t *testing.T) {
	t.Setenv("SGOTEL_SENDGRID_PUBLIC_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("want error on missing public key")
	}
}

func TestLoad_BadRedactMode(t *testing.T) {
	t.Setenv("SGOTEL_SENDGRID_PUBLIC_KEY", "dummy")
	t.Setenv("SGOTEL_REDACT_EMAIL", "obfuscate")
	if _, err := Load(); err == nil {
		t.Fatal("want error on bad redact mode")
	}
}

func TestLoad_BadQueueFullBehavior(t *testing.T) {
	t.Setenv("SGOTEL_SENDGRID_PUBLIC_KEY", "dummy")
	t.Setenv("SGOTEL_QUEUE_FULL_BEHAVIOR", "explode")
	if _, err := Load(); err == nil {
		t.Fatal("want error on bad queue-full behavior")
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("SGOTEL_SENDGRID_PUBLIC_KEY", "dummy")
	t.Setenv("SGOTEL_LISTEN_ADDR", ":9000")
	t.Setenv("SGOTEL_WEBHOOK_PATH", "/sg")
	t.Setenv("SGOTEL_SIGNATURE_MAX_AGE", "30s")
	t.Setenv("SGOTEL_QUEUE_SIZE", "16")
	t.Setenv("SGOTEL_REDACT_EMAIL", "hash")
	t.Setenv("SGOTEL_QUEUE_FULL_BEHAVIOR", "shed")
	t.Setenv("OTEL_SERVICE_NAME", "sgotel-staging")

	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ListenAddr != ":9000" || c.WebhookPath != "/sg" {
		t.Errorf("listener: %q %q", c.ListenAddr, c.WebhookPath)
	}
	if c.SignatureMaxAge != 30*time.Second {
		t.Errorf("SignatureMaxAge: %v", c.SignatureMaxAge)
	}
	if c.QueueSize != 16 {
		t.Errorf("QueueSize: %d", c.QueueSize)
	}
	if c.RedactEmail != RedactHash {
		t.Errorf("RedactEmail: %q", c.RedactEmail)
	}
	if c.QueueFullBehavior != QueueFullShed {
		t.Errorf("QueueFullBehavior: %q", c.QueueFullBehavior)
	}
	if c.ServiceName != "sgotel-staging" {
		t.Errorf("ServiceName: %q", c.ServiceName)
	}
}
