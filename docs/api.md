# API

The service listens on plain HTTP. Put it behind an HTTPS reverse proxy before
exposing a route. The service sets `Cache-Control: no-store` on every JSON
response and `X-Content-Type-Options: nosniff`.

## Authentication

`POST /webhook` and `GET /readyz` require exactly one matching header:

```text
Authorization: Bearer <INDEX01_WEBHOOK_TOKEN>
```

The header value must match the configured token. Missing, duplicate, or
mismatched values return `401 Unauthorized`. The service does not log header
values or tokens.

`GET /healthz` and `GET /statusz` are private and unauthenticated. Restrict
these routes to the local service network. Do not publish them through a
public Ingress or proxy. `/readyz` is the authenticated route for external
readiness checks.

## `POST /webhook`

The request must use `multipart/form-data`. It can contain these fields:

| Field | Required | Value |
| --- | --- | --- |
| `recordedAt` | Yes | Positive Unix time in milliseconds. |
| `client` | Yes | Sender identifier. |
| `transcription` | No | Text for DeepSeek extraction. |
| `audio` | No | Audio file for compatibility. Bytes are discarded. |

The request can include one `X-Index-Trigger` header. The receiver stores its
bounded value as metadata. Unknown fields and duplicate fields are rejected.

Audio metadata is retained. The filename and byte count enter the recording
metadata. A transient digest enters the payload fingerprint. The audio bytes
are not stored.

### Intake limits

The receiver enforces these limits from `webhook.go`:

| Limit | Maximum |
| --- | ---: |
| Complete multipart body | `INDEX01_MAX_BODY_BYTES`, default `67108864` bytes (64 MiB) |
| Multipart parts | 4 |
| `recordedAt` field | 20 bytes |
| `client` field | 128 bytes |
| `transcription` field | 65536 bytes (64 KiB) |
| Audio filename | 255 bytes |
| `X-Index-Trigger` value | 128 bytes |
| Headers per multipart part | 8 values and 4096 bytes |
| Request headers | 32 values and 16384 bytes |
| `Content-Type` header value | 256 bytes |

The body limit is configurable only through `INDEX01_MAX_BODY_BYTES`. The
other limits are fixed. A filename containing a null byte or line break is
invalid. The receiver also rejects a duplicate `Content-Type` or trigger
header.

### Success responses

A new recording is queued when transcription is present:

```json
{"id":123,"duplicate":false,"queued":true}
```

A new audio-only receipt has `queued:false`. A new accepted recording returns
`202 Accepted`. A semantic duplicate returns `200 OK` and increments its
receive count. A duplicate does not create another extraction job.

`queued` means that the recording has an extraction job that is not complete or
reviewed. It does not mean that DeepSeek extraction or TickTick delivery has
finished.

### Error responses

Every API error uses this shape:

```json
{"error":"message"}
```

The status map is:

| Status | Condition |
| ---: | --- |
| `400` | Invalid multipart structure, multipart part headers, or field value. |
| `401` | Missing, duplicate, or invalid Bearer authentication. |
| `413` | Complete body or bounded field is too large. |
| `415` | Content type is not `multipart/form-data`. |
| `431` | Request headers exceed limits or have an invalid structure. |
| `500` | The receiver cannot persist the recording. |

## `GET /healthz`

This private route runs a live SQLite health check. It returns `200` with:

```json
{"status":"ok"}
```

A database failure returns `503` with the standard error shape. This endpoint
checks the database only. It does not call DeepSeek or TickTick.

## `GET /statusz`

This private, unauthenticated route returns aggregate operational status. A
healthy report returns `200`. A degraded report returns `503`. A status query
failure returns `503` with the standard error shape.

The report can include intake counts, the latest receive time and response
class, worker state and heartbeat, queue counts and age, and the latest
provider latency and failure flag. It excludes recordings, owner values,
transcriptions, item content, provider bodies, credentials, and authorization
headers.

The service marks status degraded for a missing or stopped worker, a worker
heartbeat older than two minutes, a failed worker cycle, an active queue older
than 15 minutes, blocked authentication, review work, dead-letter work, or
provider latency above 25 seconds.

A provider `last_failed` flag alone does not necessarily degrade readiness. The
latency threshold and the other health reasons control the degraded result.

## `GET /readyz`

This route requires the same single matching Bearer header as `/webhook`. It
returns the same safe aggregate report and status behavior as `/statusz`:
`200` for `status:"ok"`, or `503` for `status:"degraded"`.

Use `/readyz` through the HTTPS proxy. Keep `/healthz` and `/statusz` on the
private service network. Use a private network policy and proxy route allowlist
as deployment controls. See the [Docker Compose guide](docker-compose.md) and
[Kubernetes guide](kubernetes.md) for deployment-specific routing.
