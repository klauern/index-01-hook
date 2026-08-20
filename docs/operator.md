# Operator reference

The binary runs `serve` when started without arguments. Operator commands use
stdout for documented results and stderr for errors. Do not put tokens or
private content in command lines, logs, or support reports.

## Command table

| Command | Environment | Contract |
| --- | --- | --- |
| `serve` | All application variables. Required provider and routing values must be set. | Start the HTTP server, worker, SQLite store, and provider clients. |
| `version` | None | Print version, commit, and build date as JSON. |
| `healthcheck` | None | Check `http://127.0.0.1:8080/healthz` and print only `{"status":"ok"}` on success. |
| `maintenance` | None | Wait as a long-running maintenance container process. |
| `ticktick-projects` | `INDEX01_TICKTICK_TOKEN` only | List safe TickTick project summaries. |
| `status ID` | `INDEX01_DB_PATH`, or its default | Print safe status for recording ID `ID`. |
| `retry-recording ID` | `INDEX01_DB_PATH`, or its default | Requeue eligible extraction work for recording ID `ID`. |
| `retry-delivery ID` | `INDEX01_DB_PATH`, or its default | Requeue eligible delivery work for delivery ID `ID`. |
| `purge-expired` | `INDEX01_DB_PATH` and exact `INDEX01_PURGE_CONFIRM` | Purge eligible terminal recordings older than 30 days. |
| `backup -` | `INDEX01_DB_PATH`, or its default | Stream a verified SQLite backup to standard output. |
| `restore PATH` | `INDEX01_DB_PATH`, or its default | Validate and install the SQLite backup at `PATH`. |
| `restore -` | `INDEX01_DB_PATH`, or its default | Read, validate, and install a SQLite backup from standard input. |

`backup PATH` is not supported. Use `backup -` for a stream. Use the deployment
wrappers for encrypted files.

The `serve` command validates configured TickTick routing during startup. The
`ticktick-projects` command does not load SQLite and does not require DeepSeek,
webhook, or routing settings.

## Retry work

Manual retry is allowed only when the target workflow state is one of:

- `blocked_auth`
- `needs_review`
- `dead_letter`

`retry-recording ID` resets the extraction queue to received. It does not
recreate a frozen extraction. `retry-delivery ID` resets the selected delivery
queue to extracted. A delivery with an ambiguous classification is reconciled
before the worker creates another item.

A completed extraction or delivery cannot enter the queue again. A completed
TickTick item is never sent again by a manual retry. Use `status ID` before a
retry and review the provider or configuration cause first.

The commands require positive numeric IDs. They return a JSON state result on
success. An ineligible ID returns an error instead of changing queue state.

## Purge

Purge is explicit. It removes recordings older than 30 days when no active or
recent delivery work protects them. It does not remove active or retrying work.
Old `blocked_auth`, `needs_review`, and `dead_letter` work can become eligible.
Review unresolved terminal work before purge.

Set the exact confirmation for one invocation:

```sh
INDEX01_PURGE_CONFIRM=purge-expired-recordings index-01-hook purge-expired
```

The command returns a JSON result with the purge state, deleted recording
count, and retention days. Do not place the confirmation in the long-running
service environment. Review an encrypted backup before purge.

## Backup

`backup -` opens the configured database read-only. It performs an online
SQLite backup that includes committed write-ahead log state. It validates the
backup before streaming it.

The command writes only SQLite backup bytes to standard output. Errors go to
standard error. Do not redirect the stream to a plaintext file. The stream can
contain transcription and extracted content.

For Compose, use the [backup wrapper](../scripts/compose-backup.sh) through the
[Docker Compose guide](docker-compose.md). The wrapper encrypts the
stream with `age` and writes a checksum. For Kubernetes, use the approved [`backup-export` Task](../Taskfile.yml) through
the [Kubernetes guide](kubernetes.md). Keep the age
identity separate from the host and repository.

## Restore

Stop the application before any restore. Do not restore while the worker or
HTTP server can write to the database.

`restore PATH` reads the exact file path supplied as the argument. `restore -`
reads the backup bytes from standard input. Both forms validate SQLite
integrity and application database identity before replacement.

The command preserves existing database, `-wal`, and `-shm` files with a
`.pre-restore-<timestamp>` suffix. It installs the validated database with
protected file permissions. Standard input is staged in a protected temporary
file and removed after the operation.

The restore command does not decrypt backups. Decrypt and checksum the backup
through the deployment wrapper, then pass the verified SQLite bytes to the
operator command. The [Compose restore wrapper](../scripts/compose-restore.sh), [Docker Compose
guide](docker-compose.md), and [Kubernetes guide](kubernetes.md) define the
stop, restore, restart, and health sequence.

## Safe output boundaries

- `version` prints only build metadata.
- `healthcheck` prints only a fixed success result.
- `ticktick-projects` prints only `id`, `kind`, `closed`, and `writable`.
- `status` prints queue state, hashes, markers, provider identifiers, and task identifiers. It does not print transcription, titles, notes, tokens, or provider bodies.
- Application status endpoints print aggregate values and safe reason codes only.
- Provider errors retain classifications and safe status data. They do not retain provider bodies or credentials.
- `backup -` is sensitive binary output. Protect it and encrypt it immediately.

See [configuration](configuration.md) for environment boundaries and
[security policy](../SECURITY.md) for vulnerability reporting.
