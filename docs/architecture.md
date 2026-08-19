# Architecture

Index 01 Hook has one process, one worker, and one SQLite database. The process
runs the HTTP receiver and the worker. The worker uses durable SQLite queues.

## Data flow

1. Index 01 sends an authenticated `multipart/form-data` request.
2. The receiver validates bounded fields and headers.
3. The receiver streams audio to a discard sink.
4. The receiver computes a payload fingerprint and stores the receipt in SQLite.
5. A transcription creates one extraction job.
6. The worker claims the job with a durable lease.
7. DeepSeek extracts zero to ten independent tasks or notes.
8. SQLite freezes the validated extraction before delivery starts.
9. The worker claims each delivery task separately.
10. TickTick creates each task or note.
11. The worker records the provider identifier and delivery result.
12. A terminal retention operation purges eligible old recordings.

A request without transcription can be retained as an audio-only receipt. It
does not create an extraction job.

## Intake and deduplication

The receiver retains `recordedAt`, `client`, the trigger header, transcription,
audio filename, audio byte count, and a payload fingerprint. The fingerprint
covers the normalized request fields and the transient SHA-256 audio digest.
The raw audio bytes and the transient digest are not stored as separate data.

Equivalent payloads share one recording. A duplicate receipt increments the
receive count and does not create another extraction job. The first receipt
returns `202`; a duplicate receipt returns `200`.

## SQLite queue

SQLite uses one connection and write-ahead logging. The database contains the
recording, extraction, delivery, attempt, and worker-health records. The queue
survives process restarts.

Extraction uses `received`, `extracting`, `extracted`, `retry_wait`,
`blocked_auth`, `needs_review`, `dead_letter`, and `complete`. Delivery uses
`extracted`, `creating`, `retry_wait`, `blocked_auth`, `needs_review`,
`dead_letter`, and `complete`.

The worker claims extraction before delivery. It processes one claim per cycle.
Each claim stores its owner and lease expiration in SQLite. The lease duration
is two minutes. An expired lease becomes claimable by the same worker after a
restart or a stalled provider call.

The service allows one active worker. Do not run another process against the
same database. Do not run more than one Compose container or Kubernetes
replica.

## DeepSeek extraction

The worker sends only the retained transcription to DeepSeek. It also sends a
system prompt with the current date, configured IANA time zone, configured task
alias names, and the strict extraction schema.

The worker does not send audio, audio metadata, the Index 01 client, the
trigger, or the database fingerprint to DeepSeek. DeepSeek returns structured
items. Each item is a task or note with validated fields.

The service stores the provider name, configured model, and optional provider
response identifier with the frozen extraction. Frozen output is immutable.
A later retry does not replace a successful frozen extraction.

## TickTick delivery

The worker sends each frozen item independently to TickTick. A task request
contains the title, content, marker, priority, tags, due date, all-day flag,
and configured time zone. The service selects the project from the default
route or a configured alias.

A note request contains the title, content, and marker. The service selects the
configured note project. A note cannot select an alias or `inbox`.

The marker uses the recording fingerprint and item index. It supports
reconciliation after an ambiguous create request. The worker can read a note
back to confirm its kind, project, and marker.

The worker reads the selected TickTick project during reconciliation. It
compares the returned kind, marker, title, and content with frozen data. It does
not send the source transcription.

## Failure and retry states

DeepSeek authentication failures enter `blocked_auth`. Refusals enter
`needs_review`. Malformed or terminal responses enter `dead_letter`. Retryable
responses use bounded exponential retry and then enter `dead_letter` after the
configured attempt limit.

TickTick authentication failures enter `blocked_auth`. Configuration failures
enter `needs_review`. Malformed create responses enter `dead_letter`.
Malformed reconciliation responses enter `needs_review`. Transport ambiguity
triggers reconciliation. Retryable and unconfirmed results retry
within bounded limits. An unresolved ambiguity enters `needs_review`.

A completed TickTick item is not sent again. Manual retry applies only to
`blocked_auth`, `needs_review`, and `dead_letter` work. Retry preserves an
ambiguous delivery classification so the worker reconciles it before another
create request.

## Transcription erasure and retention

If DeepSeek extracts no items, SQLite clears the transcription when extraction
becomes complete. If every delivery task completes, SQLite clears the
transcription and completes the recording queue.

The filename, byte count, payload fingerprint, receipt metadata, and durable
status remain until retention removes the recording. SQLite and backups can
retain extracted titles, notes, and transcription text while work is active or
not fully delivered.

`purge-expired` is explicit. It removes recordings older than 30 days when no
active or recent delivery work protects them. It does not purge active or
retrying work. Old `blocked_auth`, `needs_review`, and `dead_letter` work can
become eligible. Review unresolved terminal work before purge.

## Health and readiness

`/healthz` checks a live SQLite query. `/statusz` and `/readyz` report aggregate
worker, queue, intake, and provider data. A missing, stopped, stale, or failed
worker can make the report degraded. A queue older than 15 minutes, blocked
work, review work, dead-letter work, or provider latency above 25 seconds can
also make the report degraded.

A provider `last_failed` flag alone does not necessarily degrade readiness.
Provider latency and queue or worker conditions determine the health reasons.

See the [API reference](api.md) for endpoint behavior and the [operator
reference](operator.md) for recovery actions.
