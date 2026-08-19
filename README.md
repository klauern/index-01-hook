# Index 01 Hook

Index 01 Hook receives authenticated Index 01 multipart webhooks. It sends
transcriptions to DeepSeek for extraction. It sends extracted tasks and notes
to TickTick.

Only DeepSeek and TickTick are supported providers.

## Release status

Docker Compose files and portable Kubernetes files exist. Continuous
integration (CI) and gated release automation exist. Source publication, history
cleanup, the first public release, and final validation remain pending. The source
repository and public issue workflow remain private pending source publication.

Do not treat local images or evaluation images as release artifacts.

## Quickstart with Docker Compose

Docker Compose is the primary deployment method. The build is local. Service
startup validates TickTick routing but does not create a TickTick item.

1. Build the local image. This step needs no provider credentials.

   ```sh
   docker build --tag index-01-hook:local .
   ```

2. Configure [DeepSeek](docs/provider-setup.md#1-configure-deepseek) and
   [TickTick](docs/provider-setup.md#2-configure-ticktick). Do not configure the
   Index 01 webhook yet.
3. Copy and protect the environment file.

   ```sh
   cp -f .env.example .env
   chmod 0600 .env
   ```
4. Fill every required empty value in `.env`.
5. Start the service from the image built in step 1.

   ```sh
   docker compose --env-file .env up -d --no-build
   ```

6. Check the local health endpoint.

   ```sh
   docker compose --env-file .env exec -T index-01-hook /index-01-hook healthcheck
   ```

7. Configure HTTPS through the full [Docker Compose guide](docs/docker-compose.md).
8. Configure [Index 01](docs/provider-setup.md#3-configure-index-01). Run the
   optional provider-writing test only with synthetic text.

Keep the service behind the HTTPS proxy. Publish only the documented public
routes. Do not publish `/healthz` or `/statusz`.

## Service model

The service uses one process, one background worker, and one SQLite database.
SQLite stores the durable extraction and delivery queues. Do not scale the
Compose service or the Kubernetes Deployment.

The receiver authenticates each webhook with one configured Bearer token. The
worker freezes DeepSeek output before it creates TickTick items. It retries
recoverable failures and reconciles uncertain TickTick requests.

Audio bytes are streamed and discarded. SQLite retains the audio filename,
byte count, and a digest-derived payload fingerprint. SQLite and backups can
contain transcription text and extracted content.

Use transcription-only delivery when audio is not required. Audio support is
for sender compatibility. The service does not store audio files.

## Supported behavior

Each transcription can produce zero or more independent TickTick tasks and
notes. The service does not continue conversations, update existing items, or
send prior messages to DeepSeek.

The default task destination can be a writable TickTick task project or the
reserved `inbox` value. Notes use the configured writable note project. Aliases
select additional task projects. See the [configuration reference](docs/configuration.md)
and [architecture reference](docs/architecture.md).

## HTTP surface

The public HTTPS surface should expose only `POST /webhook` and authenticated
`GET /readyz`. Keep `GET /healthz` and `GET /statusz` on the private network.
All JSON responses use `Cache-Control: no-store`.

A new accepted webhook returns `202`. A semantic duplicate returns `200`.
The [API reference](docs/api.md) defines authentication, request limits, error
responses, and readiness thresholds.

## Operating boundaries

The service stores queue state and content in SQLite. Backups can contain the
same content. Protect the database volume and every backup encryption identity.

Provider failures delay work. They do not cause unbounded retries. Review
blocked authentication, review, dead-letter, and ambiguous delivery states.
Use the [operator reference](docs/operator.md) for safe commands.

## Local development

Use the direct binary only for local development and tests. The binary does
not provide TLS.

```sh
go test ./...
go build -o index-01-hook .
```

Run the complete local test suite with `task test`. The optional live DeepSeek
test uses synthetic input and is not part of the normal test suite.

## References

- [Architecture](docs/architecture.md): data flow, queue states, leases, and retention.
- [Configuration](docs/configuration.md): application and deployment inputs.
- [API](docs/api.md): endpoints, authentication, limits, and responses.
- [Operator](docs/operator.md): commands, retries, purge, backup, and restore.
- [Provider setup](docs/provider-setup.md): DeepSeek, TickTick, and Index 01 setup.
- [Docker Compose](docs/docker-compose.md): primary deployment and HTTPS operations.
- [Kubernetes](docs/kubernetes.md): advanced portable cluster deployment.
- [Deployment reference](docs/deployment.md): packaging and release safety gates.
- [Public validation](docs/public-validation.md): opt-in synthetic and disposable tests.
- [Public support contract](docs/public-support-contract.md): support scope and limits.
- [Security](SECURITY.md): vulnerability reporting and supported versions.
- [Support](SUPPORT.md): support requests and safe diagnostic data.
- [Contributing](CONTRIBUTING.md): contributor workflow and quality checks.
- [Third-party notices](THIRD_PARTY_NOTICES.md): dependency and artifact notices.
- [License](LICENSE): MIT License terms.
