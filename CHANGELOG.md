# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial scaffold: SendGrid Signed Event Webhook receiver with ECDSA verification
- OTel logs (one record per event) and metrics (low-cardinality counters/histograms)
- OTLP export over http/protobuf or grpc, configured via standard OTel env vars
- Resource attributes: `service.name=sgotel`, `messaging.system=sendgrid`
- Configurable email redaction (`none`, `hash`, `drop`)
- Bounded internal queue with `block` or `shed` policies for backpressure
- Helm chart, Docker image, GitHub Actions CI/release/PR-image workflows
