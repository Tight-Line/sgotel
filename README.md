# SGOtel

> Welcome to the Skotel California.

SGOtel ("skotel") is a small HTTP service that receives [SendGrid Event
Webhook](https://www.twilio.com/docs/sendgrid/for-developers/tracking-events/event)
POSTs, verifies their ECDSA signatures, and republishes each event as OpenTelemetry
**logs** (one record per event, full fidelity) and **metrics** (low-cardinality
counters and histograms for dashboards).

## Why logs *and* metrics, no spans

SendGrid webhook events are discrete records that arrive asynchronously and
sometimes hours apart. That maps cleanly onto OTel logs: one record per event,
all fields preserved as attributes. It does *not* map cleanly onto traces,
because there is no well-defined "end" to an email's lifecycle and events
routinely arrive out of order.

Logs preserve every field for forensic queries ("why did this specific email
bounce?"). Metrics are derived in parallel for dashboards and alerts, but with
cardinality kept bounded (no `email` or `sg_message_id` in metric labels).

## Architecture

```
SendGrid → POST /webhook → [verify ECDSA sig + timestamp window]
                                  ↓
                          [parse JSON array]
                                  ↓
                        [bounded channel] ── 200 OK back to SendGrid
                                  ↓
                       [publisher workers]
                                  ↓
                ┌─────────────────┴─────────────────┐
                ↓                                   ↓
        OTel Logs (per event)             OTel Metrics (counters)
                └─────────────────┬─────────────────┘
                                  ↓
                       OTLP exporter (http/grpc)
```

The handler does verification and parsing synchronously (failures must surface
to SendGrid as non-2xx) and then enqueues events to a bounded channel before
returning 200. The publisher's worker goroutines drain the channel.

## Event → OTel mapping

### Logs

| Field                       | Source                                                      |
|-----------------------------|-------------------------------------------------------------|
| `Timestamp`                 | SendGrid `timestamp` (Unix seconds)                         |
| `ObservedTimestamp`         | Receive time at SGOtel                                      |
| `Severity`                  | `bounce`/`dropped`/`spam_report` → ERROR; `deferred` → WARN; everything else → INFO |
| `EventName`                 | `sendgrid.<event>`                                          |
| `Body`                      | `"<event> <email>"` (email subject to redaction)            |
| `sendgrid.event`            | event type                                                  |
| `sendgrid.event_id`         | `sg_event_id`                                               |
| `sendgrid.message_id`       | `sg_message_id`                                             |
| `sendgrid.smtp_id`          | `smtp-id`                                                   |
| `sendgrid.email`            | recipient (see `SGOTEL_REDACT_EMAIL`)                       |
| `sendgrid.category`         | category array                                              |
| `sendgrid.bounce.{reason,status,type}` | bounce-only                                      |
| `sendgrid.url`              | click-only                                                  |
| `sendgrid.useragent`, `sendgrid.ip` | open/click                                          |
| `sendgrid.response`, `sendgrid.attempt` | delivery/deferred                               |
| `sendgrid.custom.<key>`     | any custom args attached at send time                       |

### Metrics

| Metric                          | Type      | Attributes                                  |
|---------------------------------|-----------|---------------------------------------------|
| `sendgrid.events.total`         | counter   | `event`, `category` (first category only)   |
| `sendgrid.bounces.total`        | counter   | `type` (hard/soft/blocked), `status_class` (2xx/4xx/5xx) |
| `sendgrid.webhook.batch.size`   | histogram | (none)                                      |
| `sendgrid.webhook.requests.total` | counter | `result` (ok / bad_signature / bad_payload / queue_full / …) |

Cross-event latency (e.g., `processed → delivered`) is intentionally out of
scope; it requires state and is fragile under out-of-order delivery. Derive it
downstream with an OTel collector connector if you need it.

## Configuration

All knobs are environment variables. Standard OTel env vars
(`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS`, etc.) are honored
by the underlying exporters.

| Variable                          | Default          | Notes                                                                 |
|-----------------------------------|------------------|-----------------------------------------------------------------------|
| `SGOTEL_LISTEN_ADDR`              | `:8080`          | Listen address.                                                       |
| `SGOTEL_WEBHOOK_PATH`             | `/webhook`       | Path for SendGrid POSTs.                                              |
| `SGOTEL_SENDGRID_PUBLIC_KEY`      | *(required)*     | Base64 PKIX DER. Copy from SendGrid → Mail Settings → Signed Webhook. |
| `SGOTEL_SIGNATURE_MAX_AGE`        | `5m`             | Reject signatures whose timestamp falls outside ± this window. Use `0` to disable. |
| `SGOTEL_REDACT_EMAIL`             | `none`           | One of `none`, `hash` (SHA-256 hex of lowercased address), `drop`.    |
| `SGOTEL_QUEUE_SIZE`               | `1024`           | Buffered channel between handler and publishers.                      |
| `SGOTEL_QUEUE_FULL_BEHAVIOR`      | `block`          | `block` waits for room (and may delay the SendGrid 200); `shed` responds 503. |
| `SGOTEL_MAX_BODY_BYTES`           | `5242880` (5 MiB)| Max request body accepted; larger POSTs get `413` before signature verification. |
| `SGOTEL_ENQUEUE_TIMEOUT`          | `5s`             | In `block` mode, how long a request waits for queue space before shedding with `503` (SendGrid retries). `0` waits indefinitely. |
| `OTEL_SERVICE_NAME`               | `sgotel`         | Standard OTel service name (identifies the relay process, not the upstream). All signals additionally carry the resource attribute `messaging.system=sendgrid` so backends can facet on it. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`     | `http/protobuf`  | Or `grpc`. Per-signal overrides (`..._LOGS_PROTOCOL`, `..._METRICS_PROTOCOL`) are honored. |
| `OTEL_EXPORTER_OTLP_ENDPOINT`     | (SDK default)    | Collector endpoint.                                                   |
| `OTEL_RESOURCE_ATTRIBUTES`        | (none)           | Standard OTel env var, format `k1=v1,k2=v2`. Merged into the resource. Use `deployment.environment.name=<env>` to distinguish per-env installs. |

### Helm values

The Helm chart at `charts/sgotel` exposes the same surface plus deployment knobs.
Highlights:

| Value                       | Default     | Notes                                                                  |
|-----------------------------|-------------|------------------------------------------------------------------------|
| `sendgridPublicKey`         | *(required)*| Base64 PKIX DER. Or set `existingSecret` to a Secret containing `SGOTEL_SENDGRID_PUBLIC_KEY`. |
| `otlp.endpoint`             | `""`        | OTLP endpoint (e.g. an in-cluster collector address).                  |
| `otlp.protocol`             | `http/protobuf` | Or `grpc`.                                                         |
| `otlp.headers`              | `""`        | Free-form `k=v,k=v` headers (stored in the Secret).                    |
| `sgotel.*`                  | (see file)  | One key per `SGOTEL_*` env var.                                        |
| `service.type`              | `ClusterIP` | Use `LoadBalancer` if exposing directly to the internet.               |
| `ingress.enabled`           | `false`     | Enables the Ingress template below.                                    |
| `ingress.className`         | `""`        | Required when `enabled`; chart fails fast otherwise.                   |
| `ingress.host`              | `""`        | Required when `enabled`. Single hostname (SendGrid points at one URL). |
| `ingress.path`              | `/webhook`  | Default path; matches the in-pod webhook route.                        |
| `ingress.tls.enabled`       | `false`     | Set when the ingress controller terminates TLS.                        |
| `ingress.tls.secretName`    | `""`        | TLS Secret name (cert-manager will create it, or pre-provision it).    |

When fronting sgotel with gatekeeper, leave `ingress` disabled and have gatekeeper
route a `sendgrid`-typed verifier to `http://<release-name>.<namespace>.svc.cluster.local`.

## Why no `sg_event_id` dedup

In the happy path SendGrid only re-POSTs an event when it receives a non-2xx
response. SGOtel returns 200 as soon as the event is enqueued, so SendGrid does
not retry. The remaining theoretical duplication source (a captured payload
replayed by a third party) is closed by the timestamp-window check on the
signature, which is stateless and cheaper than any in-process dedup table.

Duplication caused by SGOtel's own OTLP exporter retries is an OTLP-layer
concern handled at the collector or backend, not by `sg_event_id`.

## Running locally

```sh
export SGOTEL_SENDGRID_PUBLIC_KEY="<base64 PKIX from SendGrid UI>"
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
go run ./cmd/sgotel
```

Health check is at `GET /healthz`.

## Testing

```sh
go vet ./...
go test -race ./...
```

Tests are unit + handler-integration only; no live SendGrid, no live OTel
collector required. The handler test uses an in-memory sink so it exercises
verification, parsing, queueing, and the request/result metrics path end-to-end.
