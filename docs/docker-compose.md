# Docker Compose deployment

This guide deploys one running `index-01-hook` application container with one
named SQLite volume. The container serves plain HTTP on a private loopback port. Caddy terminates
public TLS outside the container.

Use local builds for evaluation only. Local evaluation builds are not supported
immutable release images. Do not use a tag alone or a floating public image tag
for production. Use a published release image only with an immutable digest.

## Prerequisites

Install Docker Engine with the Docker Compose plugin. Install `age` and
`shasum` for encrypted database backups. Run Caddy on the same Docker host as
Compose.

The host must have inbound ports `80` and `443` available for Caddy. The host
must resolve the public hostname to Caddy. Caddy runs on this same host and
proxies to the documented loopback upstream `127.0.0.1:8080`. Port `8080` is a
private loopback binding.

## Create the environment

Copy the example file. Fill every required empty value. Protect the file.

```sh
cp -f .env.example .env
chmod 0600 .env
```

Set `INDEX01_HOST_PORT` only when another local service uses port `8080`.
Keep `INDEX01_IMAGE=index-01-hook:local` for local builds. Compose uses fixed
application values for the database path and listen address.

Compose environment values are visible to Docker administrators. Use a host
with trusted Docker administrators. Do not commit `.env` or any `.env.*` file.

## Local build and start

Raw `docker compose` commands are local-only. Use the local path for evaluation.
The production guard rejects local tags before production Compose operations.

Build the local image and start the service in the background. Startup reads
TickTick project metadata to validate routing. Startup does not create an item:

```sh
docker compose --env-file .env build
docker compose --env-file .env up -d
```

The service listens on `127.0.0.1:${INDEX01_HOST_PORT}`. If `INDEX01_HOST_PORT`
changes, change both Caddy upstreams in `deploy/compose/Caddyfile.example`.
The service writes the SQLite database to the durable named volume at
`/var/lib/index-01-hook/data/index01.db`. The image initializes a fresh volume
for UID and GID `65532`.

If you reuse a volume, confirm that UID and GID `65532` can write its data.
The application fails closed when ownership is incompatible. Do not run the
application as root. If an incompatible volume has no required data, remove the
volume and let Docker create it again. If the volume has required data, make
and verify a backup before an administrator corrects its ownership.

Do not run `docker compose up --scale` for local evaluation. SQLite and the worker require one fixed
container.

## Logs and health

Show recent logs:

```sh
docker compose --env-file .env logs --tail=100 index-01-hook
```

Run the Compose-derived health command from the host:

```sh
docker compose --env-file .env exec -T index-01-hook /index-01-hook healthcheck
```

Check the container health state:

```sh
docker compose --env-file .env ps
```

The Compose healthcheck calls `index-01-hook healthcheck`. The command requests
only `http://127.0.0.1:8080/healthz`, rejects redirects, and accepts only HTTP
status `200`.

## Public HTTPS

### Caddy prerequisites

Install Caddy as a host service before you configure this site. Use the [official
Caddy installation guide](https://caddyserver.com/docs/install) for the host
operating system. Point the public DNS name to this host, allow inbound TCP ports
`80` and `443`, and confirm that the authorized operator can update the Caddy
configuration and reload the service. The operator must choose the required
privilege method; this guide does not assume passwordless `sudo`.

Copy the example to a working file. Edit the working file with an authorized
editor. Replace `your-hook.example.com` with the public hostname. Refuse to
install a file that still contains the example hostname:

```sh
cp -f deploy/compose/Caddyfile.example Caddyfile.local
# Edit Caddyfile.local with the authorized editor.
if grep -F 'your-hook.example.com' Caddyfile.local; then exit 1; fi
caddy validate --config Caddyfile.local
install -m 0644 Caddyfile.local /etc/caddy/Caddyfile
systemctl reload caddy
rm -f Caddyfile.local
```

Run these commands as an authorized operator. Adapt them to the host service
and privilege workflow.

Caddy obtains and renews the certificate. The example proxies only exact
`POST /webhook` and exact `GET /readyz` requests to `127.0.0.1:8080`. Every
other public request returns `404`. The example also rejects webhook bodies above
64 MiB at Caddy before forwarding them. Configure a per-client rate limit of 10 webhook requests per minute with a
burst of 20 at the public proxy or an approved edge service. Stock Caddy has no built-in rate limiter, so install an approved
rate-limit module or place a rate-limiting edge before Caddy. Keep the application
body limit at 64 MiB as a second control. Configure the webhook sender to use the
public HTTPS URL and the same webhook token as `.env`.

## First provider-free webhook request

After Caddy is ready, send a provider-free webhook. Load the token from the
protected environment before running this command. Do not paste the token into
shell history.

```sh
curl --fail --silent --show-error \
  --header "Authorization: Bearer ${INDEX01_WEBHOOK_TOKEN}" \
  --form recordedAt=1760000000000 \
  --form client=manual-test \
  --write-out '\n%{http_code}\n' \
  https://your-hook.example.com/webhook
```

The first request returns HTTP `202` with `queued` set to `false`. Repeating the
same request returns HTTP `200` as a duplicate. This request sends
only `recordedAt` and `client`; it makes no provider call and writes nothing to
TickTick. It stores one internal receipt. Explicit retention can remove the receipt
after it becomes eligible.

## Backup

Back up the live database through the operator command. Encrypt the stream
with the age recipient. Keep the age identity separate from the host and from
the repository.

```sh
AGE_RECIPIENT=age1examplechangedbeforeuse
backup=/secure/backups/index01-$(date -u +%Y%m%dT%H%M%SZ).db.age
./scripts/compose-backup.sh "$AGE_RECIPIENT" "$backup"
(cd "$(dirname "$backup")" && shasum -a 256 -c "$(basename "$backup").sha256")
```

The script streams from the running container and writes no plaintext host file.
It rejects unsafe output names and refuses to replace an encrypted artifact or
checksum. It publishes the checksum before it publishes the encrypted artifact.
Keep more than one backup generation and keep at least one copy on another
system.

## Restore

Stop the application before restore. Do not restore while the worker or HTTP
server is running.

The command below uses a temporary operator-only Compose service in the
`maintenance` profile. It does not start a worker. It mounts the same named volume.
The script validates the production image before every Compose operation.

```sh
./scripts/compose-restore.sh \
  /secure/keys/index01.age \
  /secure/backups/index01-backup.db.age \
  /secure/backups/index01-backup.db.age.sha256
```

The backup directory must permit protected hard-link snapshots. The script
creates race-resistant encrypted snapshots, then copies the encrypted input and
checksum into a protected temporary directory. It stops the application before checksum validation and decryption.
It then decrypts the complete copied backup to a mode-0600 temporary file. It runs the
operator restore command through a profile-gated maintenance service. The
maintenance service receives only `INDEX01_DB_PATH`, has no network, and receives
no application secrets. The service uses the same named volume.

The operator restore command validates the SQLite backup before replacement and
preserves current database files with a `.pre-restore-<timestamp>` suffix. After
validation succeeds, the script starts the application and waits up to 60 seconds
for a healthy state. If any restore or health step fails, the application stays
stopped. The script removes temporary plaintext on every exit. Verify health,
logs, queue state, and the first expected webhook after restart. File deletion
cannot guarantee physical erasure on every storage device.

## Production Compose guard

Set `INDEX01_IMAGE` to a complete registry image reference with a sha256 digest.
Run production Compose commands through `scripts/compose-production.sh`.
The guard validates the image before it runs the requested operation.
Raw `docker compose` commands are local-only.

For example, pull and start the selected production image:

```sh
./scripts/compose-production.sh pull
./scripts/compose-production.sh up -d --no-build
```

The guard also applies to logs, health checks, upgrades, rollbacks, retention,
secret rotation, removal, backup, and restore. The backup and restore scripts
invoke the same guard.

## Upgrade

For local evaluation, preserve the current local image under a unique rollback
tag. Then build and test the new source. Keep a fresh encrypted backup with a
verified checksum before the upgrade.

```sh
previous="index-01-hook:rollback-$(date -u +%Y%m%dT%H%M%SZ)"
docker image tag index-01-hook:local "$previous"
docker compose --env-file .env build --pull
docker compose --env-file .env up -d --no-build
docker compose --env-file .env exec -T index-01-hook /index-01-hook healthcheck
```

If local evaluation fails, run this command with the saved rollback tag:

```sh
INDEX01_IMAGE="$previous" docker compose --env-file .env up -d --no-build
```

Local image tags are not supported production releases. For production, keep
the current immutable digest and a verified encrypted backup. Set
`INDEX01_IMAGE` to the new immutable digest. Do not use a floating tag:

```text
INDEX01_IMAGE=ghcr.io/OWNER/index-01-hook@sha256:<image-digest>
```

Pull and start that exact image:

```sh
./scripts/compose-production.sh pull
./scripts/compose-production.sh up -d --no-build
```

## Rollback

Keep the previous image digest until the upgrade passes its retention period.
Restore the previous value in `.env`, then recreate the container. Verify the
health command before removing the previous image:

```text
INDEX01_IMAGE=ghcr.io/OWNER/index-01-hook@sha256:<previous-image-digest>
```

```sh
./scripts/compose-production.sh pull
./scripts/compose-production.sh up -d --no-build
```

If the schema changed, restore the pre-upgrade encrypted backup instead. Stop
the application before the restore.

## Secret rotation

Rotate the webhook, DeepSeek, or TickTick token at the provider first. Update
`.env`, protect it again, and recreate the container:

```sh
chmod 0600 .env
./scripts/compose-production.sh up -d --force-recreate
```

Update the webhook sender after the receiver accepts the new token. Check logs
without printing environment values or authorization headers.

## Retention

The service retains terminal recordings for 30 days. Active work remains until
it reaches a safe terminal state. Retention cleanup is never scheduled
automatically. Run `purge-expired` only by explicit operator command after backup
review:

```sh
./scripts/compose-production.sh exec -T index-01-hook \
  env INDEX01_PURGE_CONFIRM=purge-expired-recordings \
  /index-01-hook purge-expired
```

Set backup retention separately. Do not delete the only encrypted backup.

## Removal

Stop and remove the application container while keeping data:

```sh
./scripts/compose-production.sh down
```

Remove the named volume only after exporting and verifying encrypted backups:

```sh
docker volume rm index-01-hook-data
```

Remove the Caddy site and certificate only after the public webhook sender has
been disabled. Remove the local image when no rollback is required:

```sh
docker image rm index-01-hook:local
```
