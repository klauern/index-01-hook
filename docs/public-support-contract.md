# Public support contract

This document defines the target public release boundary for `index-01-hook`.
It applies to public releases and supported deployments.

## Users and supported platforms

The service is for operators who receive Index 01 webhooks and turn
transcriptions into TickTick tasks and notes.

Supported deployment platforms are:

- Docker Compose on a Linux host with Docker Engine and Compose v2.
- Kubernetes with a persistent volume, an ingress or reverse proxy, and one
  application replica.

Docker Compose is the primary deployment method. Kubernetes is the advanced
deployment method for operators who need cluster management.

Each supported deployment has one running application process, one replica,
and one SQLite database. Stop the application for restore, replacement, and
other write-exclusive maintenance. Online backup and explicit purge can use
the documented live wrappers. Do not run a second application process against
the same database.

## Source and image distribution

The target public source repository is:

`https://github.com/klauern/index-01-hook`

This URL becomes active after source publication. The current release status
must state when the repository is not yet public.

Gated release automation exists, but public container image publication remains
pending the first approved release. No public GHCR artifact exists until that work
completes. Do not claim that a GHCR image is available.

Production deployments must use an immutable image digest for a supported
deployment. Do not use a tag alone for production. Do not use a floating
production tag. Local Compose builds support evaluation only. A local evaluation
build is not a supported immutable release image.

A public release requires a GHCR container artifact, Docker Compose deployment
files, and portable Kubernetes deployment instructions. The Compose files support
local evaluation and the supported immutable image workflow. The Kubernetes files
are portable advanced deployment files; image publication remains pending.

The source repository is the source of truth for code, configuration names,
and deployment documentation. The container image must identify its source
release or commit.

## Providers and external accounts

DeepSeek and TickTick are the only supported providers.

A supported deployment requires:

- An Index 01 source that can send authenticated webhooks.
- A DeepSeek account and API token for task and note extraction from
  transcription text.
- A TickTick account and Open API token for item delivery.
- One open, writable TickTick `TASK` project for tasks, or the reserved Inbox
  as the default task destination.
- One open, writable TickTick `NOTE` project for notes.

The operator must provide the tokens and required project identifiers. The
reserved Inbox needs no project identifier. The service does not provide
provider accounts, tokens, projects, or provider billing.

Other language-model, task, notes, transcription, and delivery providers are
not supported by this contract.

## Deployment and persistence

Docker Compose deployments must use a durable named volume for the SQLite
database. A fresh named volume initializes for UID and GID `65532`. A reused
volume with incompatible ownership must fail closed. The volume must remain
available across container replacement and must be writable by the service
account. A durable host directory is also acceptable when the deployment
documents its ownership and backup controls.

Kubernetes deployments must use a persistent volume for the SQLite database.
The workload must use one replica. The deployment must prevent two processes
from writing the same database during updates. Supported mutation and restore
workflows must use the project maintenance Lease. A failed restore must retain
the Lease until an operator completes an approved recovery.

SQLite is the only supported application database. External databases,
shared database services, and database migration services are not supported.
Ephemeral storage is not suitable for a supported deployment.

The operator owns backups. The operator must encrypt backups, protect backup
keys, verify checksums, and test restoration. A Compose backup must reject unsafe
output names and publish its checksum before its encrypted artifact. A Compose
restore must stop the application before checksum validation and decryption. The
restore must decrypt completely, validate through the operator command, start the
application, and wait up to 60 seconds for health. A restore failure after
workload shutdown begins keeps the application stopped. The profile-gated Compose maintenance service must receive only
`INDEX01_DB_PATH`, have no network, and receive no application secrets. A Kubernetes
restore must hold the maintenance Lease through Pod removal, database replacement,
and healthy application rollout. The
project does not provide hosted backups or recovery of lost data. A backup can
contain the SQLite data that existed when the backup ran. File deletion cannot
guarantee physical erasure on every storage device.

## HTTPS and network boundary

The service listens on plain HTTP inside the private deployment network. For
Compose, Caddy must run on the same host and use host ports `80` and `443`. Caddy
must proxy to the loopback upstream `127.0.0.1:8080`. An HTTPS reverse proxy must
terminate TLS before it forwards public webhook requests.

The operator must use a valid certificate, restrict public routes to the
required endpoints, and keep operational endpoints private when possible. The
public proxy must reject webhook bodies above 64 MiB and apply a per-client rate
limit of 10 webhook requests per minute with a burst of 20.
The Kubernetes baseline must block private-address HTTPS egress. Provider-only
egress requires an operator-managed proxy or FQDN-aware network policy.
The webhook bearer token remains required after proxying. Do not expose the
service directly over public HTTP.

## Public routes

Only these routes are public:

- `POST /webhook`, protected by the webhook bearer token.
- Authenticated `GET /readyz`, protected by the same webhook bearer token.

Keep `/healthz` and `/statusz` private. Do not publish either route through a
public ingress or reverse proxy.

## Security and privacy

Operators must store webhook and provider credentials in a secret store or a
protected environment file. Operators must not commit credentials or include
credentials in logs, images, backups, or support reports.

The service sends transcription text to DeepSeek for task and note extraction.
The service sends extracted tasks and notes to TickTick. Operators must confirm
that these transfers meet their privacy and provider requirements.

The service does not retain uploaded audio as application data. SQLite can
contain webhook metadata, processing state, transcription content during
processing, extracted content, and delivery results. Backups can contain the
same data. Protect the database and every backup as private data.

The project does not provide identity management, tenant isolation, data
classification, legal compliance, or provider data-processing agreements.
The operator remains responsible for access control, retention, deletion,
secret rotation, and incident response.

## Versioning and compatibility

Releases use semantic version numbers in the form `MAJOR.MINOR.PATCH`.
Release notes define changes that affect configuration, data, or deployment.

Before version 1.0, a minor release can contain a breaking change. After
version 1.0, major releases can contain breaking changes. Patch releases fix
bugs without intentionally changing the public support contract.

The supported configuration is the configuration documented for the selected
release. Operators must review release notes before upgrades and keep a
verified backup before database or deployment changes.

Keep the current image reference and a verified encrypted backup during an
upgrade. Verify health before removing the previous image. If an upgrade fails,
restore the previous immutable image reference and restart the deployment. If a
schema change prevents rollback, restore the verified backup while the
application is stopped. Start the application only after a successful restore.

Support covers only the latest non-prerelease GitHub Release. Support does not
cover older releases, prerelease versions, modified images, unsupported providers,
custom infrastructure, provider outages, or data recovery without a usable
backup.

Report reproducible defects and security concerns through the project GitHub
repository. Include the release version, deployment method, and safe logs.
Do not include tokens, transcription text, task content, or other private data.

## Unsupported configurations

The following configurations are outside this contract:

- More than one application replica or more than one process worker.
- Shared, network, ephemeral, or externally managed SQLite access.
- An external database such as PostgreSQL or MySQL.
- A provider other than DeepSeek or TickTick.
- Public HTTP without HTTPS proxying and TLS termination.
- A deployment without durable storage and operator-managed backups.
- Floating or locally modified production images.
- High availability or automatic failover.
- Serverless, multi-tenant, or active-active deployments.
- Native host-service deployments outside Docker Compose and Kubernetes.

## Public contract and maintainer configuration

This document defines public defaults only. It contains no personal hostnames,
cluster details, registry credentials, project identifiers, secrets, or
production values.

Maintainer deployment configuration does not expand this public support
contract. The Kubernetes files are portable advanced deployment files. Operators
must adapt cluster integrations, storage classes, ingress controllers,
certificates, and network policy controls to each environment.
