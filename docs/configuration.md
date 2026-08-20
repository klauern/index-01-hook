# Configuration

The application reads environment variables at startup. Docker Compose and
Kubernetes set deployment-specific values outside the application settings.

## Application variables

| Variable | Required | Default | Rule and use |
| --- | --- | --- | --- |
| `INDEX01_WEBHOOK_TOKEN` | Yes | None | At least 32 bytes, with no whitespace; use a cryptographically random Bearer token for `/webhook` and `/readyz`. |
| `INDEX01_DB_PATH` | No | `./index01.db` | SQLite path. Empty uses the default; whitespace-only is rejected. |
| `INDEX01_LISTEN_ADDR` | No | `:8080` | Plain-HTTP listen address. Empty uses the default; whitespace-only is rejected. |
| `INDEX01_MAX_BODY_BYTES` | No | `67108864` | Positive complete request-body limit in bytes. |
| `INDEX01_DEEPSEEK_TOKEN` | Yes | None | Non-blank DeepSeek API token. |
| `INDEX01_DEEPSEEK_MODEL` | No | `deepseek-v4-flash` | Safe provider identifier passed to DeepSeek. |
| `INDEX01_TIME_ZONE` | No | `UTC` | Valid IANA time zone for date extraction and task delivery. |
| `INDEX01_TICKTICK_TOKEN` | Yes | None | Non-blank TickTick Open API token. |
| `INDEX01_TICKTICK_DEFAULT_PROJECT_ID` | Yes | None | Writable `TASK` project identifier or `inbox`. |
| `INDEX01_TICKTICK_NOTE_PROJECT_ID` | Yes | None | Writable `NOTE` project identifier. |
| `INDEX01_TICKTICK_PROJECT_ALIASES` | No | `{}` | JSON object that maps task aliases to project identifiers. |
| `INDEX01_WORKER_OWNER` | No | `index-01-hook` | Durable lease owner. A blank value uses the default. |

Every required value must be present and non-blank. Copied example values must
not remain empty or unchanged. Empty optional values use their defaults when the
loader defines a default. Parsers reject whitespace-only values; worker owner and
aliases use their defaults when blank after trimming. Startup rejects invalid
values before the HTTP server starts.

`INDEX01_WEBHOOK_TOKEN` must be at least 32 bytes and must not contain whitespace.
Generate the token with a cryptographically secure random source, such as 32 random
bytes encoded as hexadecimal or Base64URL. The loader rejects shorter or whitespace-containing
tokens before the HTTP server starts. Synthetic values in tests are longer than this
minimum and are not production credentials.

`INDEX01_DEEPSEEK_MODEL` is a safe identifier passed to DeepSeek. It can use
letters, digits, and `-_.:/`, with a maximum length of 256 bytes. The service
does not validate model availability locally. The provider can reject an
unavailable model during extraction.

`INDEX01_TIME_ZONE` must be an available IANA time zone, such as `UTC` or
`America/New_York`. `Local` is rejected. The default is `UTC`.

The default task project can be a valid open writable `TASK` project or the
reserved `inbox` value. `inbox` is valid only for the default task destination.
Project identifiers can use letters, digits, and `-_.:/`, with a maximum length
of 256 bytes. The note project and every alias must use a real open writable
project. Notes and aliases reject `inbox`. Startup checks TickTick project kind,
closed state, and write access.

## Project aliases

Set aliases as one JSON object. Each key is a case-insensitive task alias of at
most 100 bytes. Each value is a TickTick project identifier. The worker gives
only configured alias names to DeepSeek. DeepSeek cannot choose an arbitrary provider project ID.

Use a safe example with generic identifiers:

```dotenv
INDEX01_TICKTICK_PROJECT_ALIASES={"work":"project-task-001","home":"project-task-002"}
```

Use `{}` when no additional task routes are needed. Do not use an empty alias,
an empty project value, duplicate normalized keys, or `inbox`.

## Compose-only variables

These variables control the local Compose deployment. Compose does not pass
`INDEX01_IMAGE` or `INDEX01_HOST_PORT` to the application.

| Variable | Required | Default | Use |
| --- | --- | --- | --- |
| `INDEX01_IMAGE` | No | `index-01-hook:local` | Local image or immutable release image. |
| `INDEX01_HOST_PORT` | No | `8080` | Loopback host port mapped to container port `8080`. |

Keep the application database path and listen address at the values defined by
`compose.yaml`. Use the [Docker Compose guide](docker-compose.md) for TLS,
backups, restore, upgrades, and removal.

## Operator-only confirmation

`purge-expired` requires this exact environment value:

```text
INDEX01_PURGE_CONFIRM=purge-expired-recordings
```

Do not set this value in the long-running application environment. See the
[operator reference](operator.md) for the retention contract.

## Kubernetes and Task inputs

Kubernetes rendering and Taskfile operations use these inputs. They are not
application environment variables.

| Input | Required for | Rule |
| --- | --- | --- |
| `IMAGE_REF` | `render`, `dry-run`, `server-dry-run`, `deploy` | Immutable image reference with a digest. |
| `REGISTRY_ACCESS_MODE` | The same deployment tasks | `public` or `private`. |
| `KUBE_INGRESS_HOST` | The same deployment tasks | Approved fully qualified host name. |
| `KUBE_INGRESS_CLASS` | The same deployment tasks | Approved IngressClass name. |
| `KUBE_TLS_SECRET` | The same deployment tasks | Existing TLS Secret name. |
| `KUBE_STORAGE_CLASS` | The same deployment tasks | Optional approved storage class. Empty selects the cluster default. |
| `KUBE_CONTEXT` | `dry-run`, `server-dry-run`, `deploy`, `status`, `logs`, `backup-export`, `restart`, `rollback`, `withdraw-first-deploy` | Exact approved Kubernetes context. |
| `DESTINATION` | `backup-export` | Protected encrypted output path. |
| `AGE_RECIPIENT` | `backup-export` | Approved age X25519 recipient. |
| `REVISION` | `rollback` | Positive Deployment revision number. |
| `CONFIRM` | `rollback`, `withdraw-first-deploy` | Exact command-specific confirmation. |

Kubernetes application settings come from the protected
`index-01-hook-secrets` Secret. The portable manifests set the database path,
listen address, body limit, and worker owner. See the [Kubernetes guide](kubernetes.md).

## Environment visibility and secrets

Compose environment values are visible to Docker administrators and may appear
in container inspection output. Trust the Docker host and protect `.env` with
mode `0600`. Do not commit `.env` or other completed environment files.

Kubernetes stores application tokens in `index-01-hook-secrets` and loads them
with `envFrom`. Create the Secret from a protected file outside the repository.
Do not place tokens on command lines or in rendered manifests. Registry and TLS
credentials use separate protected Secrets.

The maintenance containers receive only `INDEX01_DB_PATH`. They do not receive
provider tokens. SQLite files and encrypted backups can contain transcription
and extracted content. Protect the volume, backup stream, encryption identity,
and restore staging files.
