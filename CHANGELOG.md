# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.2] - 2026-06-12

### Security

- Cap the webhook request body before reading it, so an oversized POST can no
  longer exhaust memory ahead of signature verification (`SGOTEL_MAX_BODY_BYTES`,
  default 5 MiB; responds `413`). (#8)
- Add `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to the HTTP server so a slow or
  stalled client can't hold a connection open indefinitely. (#8)

### Added

- `SGOTEL_ENQUEUE_TIMEOUT` (default `5s`): in `block` mode, bound how long a
  request waits for queue space before returning `503` so SendGrid redelivers.
  Set `0` to wait indefinitely (previous behavior). (#9)

## [0.0.1] - 2026-06-11

### Added

- Initial scaffold: SendGrid Signed Event Webhook receiver with ECDSA verification
- OTel logs (one record per event) and metrics (low-cardinality counters/histograms)
- OTLP export over http/protobuf or grpc, configured via standard OTel env vars
- Resource attributes: `service.name=sgotel`, `messaging.system=sendgrid`
- Configurable email redaction (`none`, `hash`, `drop`)
- Bounded internal queue with `block` or `shed` policies for backpressure
