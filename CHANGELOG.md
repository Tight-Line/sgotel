# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- Upgrade Alpine packages in the runtime stage of the image. `alpine:3.24.1`
  still ships `libcrypto3`/`libssl3` at 3.5.7-r0, which carries twenty open
  advisories including `CVE-2026-14456` (HIGH); the v3.24 apk repository has
  the fixed 3.5.8-r0 and no newer base image tag exists to pick it up. Running
  `apk upgrade` at build time takes patched packages whenever the repo gets
  ahead of the tag, rather than waiting for a 3.24.2 that may never ship.

## [0.0.5] - 2026-09-14

### Security

- Pin every GitHub Actions reference to a full commit SHA. All 33 `uses:`
  entries across the six workflows previously named a mutable tag such as
  `@v7`, which whoever holds the tag can repoint at any commit. The version
  stays in a trailing comment so Dependabot keeps updating them.

### Changed

- The Helm chart now declares `ephemeral-storage` alongside cpu and memory:
  request `64Mi`, limit `512Mi`. SGOtel writes no files, so this covers
  container logs and the writable layer only. **Upgraders on clusters with
  little spare node disk should check this schedules**, since a pod that
  previously made no storage claim now makes one.

## [0.0.4] - 2026-09-14

### Security

- Clear seven standard library advisories that SGOtel's own code paths reach.
  The `go` directive moves from 1.25.0 to 1.27.1, which is what determines the
  standard library a build gets: `GO-2026-6218` (quadratic complexity in
  `net/url`), `GO-2026-6091` (`html/template` Javascript regexp context
  tracking), `GO-2026-6090` (unbounded post-handshake messages in
  `crypto/tls`), `GO-2026-6089` (`ReadHeaderTimeout` skipped on the
  unencrypted HTTP/2 check in `net/http`), `GO-2026-5972` (unbounded recursion
  in `encoding/asn1`, reached through the webhook's public key parsing),
  `GO-2026-5856` (Encrypted Client Hello privacy leak in `crypto/tls`) and
  `GO-2026-5026` (ASCII-only Punycode labels accepted via `net/http`).
- Bump `golang.org/x/net` to 0.59.0 for `GO-2026-5942` and `golang.org/x/text`
  to 0.42.0 for `GO-2026-5970`. Both reach SGOtel through the OTLP exporters.
- Add a `govulncheck` job to CI and a `make vulncheck` target. Nothing scanned
  for vulnerable dependencies before, which is why the advisories above went
  unnoticed through two releases.
- Configure Dependabot for Go modules, GitHub Actions and Docker. Security
  updates arrive as their own pull requests so they can be released without
  waiting on a routine batch.

### Changed

- Update OpenTelemetry to 1.46.0 and the logs bridge to 0.22.0. The bridge now
  uses `attribute.Value` and `attribute.KeyValue` in place of its own types.
  Emitted attribute names, types and values are unchanged.
- Routine dependency updates: gRPC 1.83.2, protobuf 1.36.12, grpc-gateway
  2.30.0, logr 1.4.4, OTLP proto 1.11.0, genproto and `golang.org/x/sys`.
- Build on `golang:1.27.1-alpine` and run on `alpine:3.24.1`. CI now reads the
  Go version from `go.mod` instead of pinning it separately in each workflow.
- Update 11 GitHub Actions, including `actions/checkout` to v7, `setup-go` to
  v7 and the CodeQL actions to 3.29.11.
- Pin golangci-lint to v2.13.2 and run it through `go run` from the Makefile,
  so a contributor and CI use the same linter and it is always built by the
  toolchain the `go` directive selects. A linter built by an older Go than the
  one it targets refuses to start, which Go 1.27 made a hard failure.
- Widen the `coverage:ignore` lookback in `scripts/check-coverage.sh` from one
  line to two. Go 1.27 attributes an uncovered `if` body to the body's first
  statement rather than to the `if`, which put the established comment
  placement out of range at nine sites. No coverage comments moved. Reported
  coverage falls from 72.6% to 68.7% because 1.27 counts the `if` and its body
  as separate blocks, so the ignored defensive branches weigh more; the gate
  fails on uncovered lines rather than a percentage.

## [0.0.3] - 2026-08-03

### Security

- Bump google.golang.org/grpc to 1.83.0 (SNYK-GOLANG-GOOGLEGOLANGORGGRPCINTERNALTRANSPORT-18172578)

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
