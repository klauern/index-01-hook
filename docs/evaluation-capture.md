# Collect evaluation evidence

The receiver can retain private evaluation evidence before normal queue cleanup removes transcripts.
Capture is disabled by default. Minipc enables 90-day retention and six-hour destination polling.

## Configuration

| Setting | Default | Allowed values |
| --- | --- | --- |
| `INDEX01_EVALUATION_RETENTION_DAYS` | `0` | Integer from `0` through `365` |
| `INDEX01_EVALUATION_POLL_INTERVAL` | `6h` when capture is enabled | `0`, or a duration from `1h` through `168h` |

Zero retention disables new capture and polling. Previously captured evidence still expires.
Zero polling disables automatic TickTick reads. Capture remains active when retention is positive.

The archive stores these facts:

- Original transcript and recording fingerprint at receipt, including inputs whose extraction fails.
- Exact processing clock, time zone, system prompt, schema, prompt and schema hashes, and build commit.
- Successful model output, provider/model identifiers, and the number of extracted items.
- Validated aliases and default task and note destinations used for that recording.
- Successful delivery task IDs, original project IDs, and stable delivery markers.
- Latest verified or unresolved destination observation and its timestamp evidence.

Failed provider responses are not retained. Audio is not retained.
Completed transcripts from before capture was enabled cannot be recovered by this feature.

## Retention and access

The archive lives in the same private SQLite database as the queue.
Queue cleanup does not remove unexpired archive records. Archive expiry does not extend when extraction or delivery retries.
The receiver removes expired records hourly. Exports exclude expired records.
Disabling capture does not erase existing records early or extend their expiry.
Encrypted database backups also contain the archive and follow the backup retention policy.

Export files are private and must stay outside Git. The dashboard and ordinary status endpoints do not expose archive content.
Operator commands require local database access. There is no new network export endpoint.

## Collection and export

The receiver polls once at startup, then every configured interval.
An empty delivery archive makes no TickTick requests.
The collector reads accessible project task lists and checks stable task IDs and exact markers.
It checks the last verified destination, retained destination watermark, and original project for completed tasks omitted from task lists.
It never changes TickTick tasks or invokes a model.

If any required API read fails, the collector preserves the previous observation batch.
Missing tasks, conflicting markers, duplicate IDs, and stale timestamps cannot establish a corrected route.
A task moved and completed before any poll can remain unresolved if TickTick omits it from project lists.
Moves completed and reversed between polls can be missed.

Run these commands inside the receiver environment:

```sh
/index-01-hook evaluation-status
/index-01-hook evaluation-collect
/index-01-hook evaluation-export /var/lib/index-01-hook/evaluation/evidence-private.json
```

`evaluation-status` prints aggregate archive counts.
`evaluation-collect` performs one immediate read-only poll and prints aggregate counts.
`evaluation-export` requires a new output path and creates the file with mode `0600`.
Use a private path on the writable data volume. Export files do not expire with database records; remove copied exports when no longer needed.
Its output is compatible with the feedback corpus importer. It also preserves failed and zero-item evidence in the private archive envelope.

After copying an export to a private local directory, build and audit the corpus:

```sh
task eval-corpus-build INPUT=/private/evaluation-private.json OUTPUT=/private/corpus.json
task eval-corpus CORPUS=/private/corpus.json REPORT_DIR=/private/audit
task eval-corpus-replay CORPUS=/private/corpus.json REPORT_DIR=/private/replay
```

Use the actual private paths on your machine. Each recording carries its original clock and routing configuration.
Unchanged task destinations remain unlabeled. A verified move supplies an inferred expected route under the owner's default policy.
Review exceptional moves and approve unchanged examples before using them as reference cases.
Each export starts from captured observations. Keep reviewed labels in the curated corpus or previous feedback ledger when importing later exports.

Replay scores the archived model output against current labels. It can expose historical routing mistakes without new model calls.
Replay cannot measure a revised prompt. Use development examples to revise prompts, then evaluate independent predictions on held-out examples.
The live evaluation command requires explicit model-call opt-in and a call budget.
No automatic prompt tuning or production routing change occurs when the collector detects a move.

## Deployment and rollback

New deployments require a webhook secret of at least 32 bytes.
An existing sender can use `INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN=true` during a coordinated upgrade.
This explicit setting accepts existing secrets of at least 14 bytes. It does not change the stored secret or authentication comparison.
Leave the setting disabled for new installations. Remove it after changing the sender and receiver to a secret of at least 32 bytes.

Migration `007` adds archive tables. Migration `008` preserves the original capture deadline after evidence cleanup.
Existing rows without retained evidence cannot be recaptured during upgrade.
Before upgrade, preserve a consistent database backup and its compatible image.
An older image cannot open the upgraded database. Restore the matching backup before using that image. Existing receiver, dashboard, and operator binaries require their compiled database schema.
Deploy the same new image to the receiver and dashboard. Enable capture and polling only on the receiver.
Create and verify an encrypted backup before starting the new receiver.

Do not roll an old image back onto a migrated database. Use a compatible build, or restore the pre-upgrade backup with the matching old binary.
A backup restore can discard recordings received after the backup. Stop writes and assess that loss before a restore.

After deployment, verify the image version, receiver readiness, dashboard access, archive status, and a private export.
An empty archive is expected before the next real recording. That state proves readiness, not end-to-end capture of a real recording.
