# Public validation

The repository has three opt-in public validation suites.
See the [local validation report](local-validation-report.md) for recorded results.

## Synthetic provider end-to-end suite

Run:

```sh
./scripts/public-experience-e2e.sh
```

The suite builds the application and a strict HTTPS provider fixture.
Internal networks map the fixed DeepSeek and TickTick hosts to the fixture.
Only Caddy joins the published network.
The application does not join the published network.

The suite sends a synthetic transcription through Caddy.
It verifies extraction, TickTick routing, durable completion, and deduplication.
It also verifies provider-free intake, route restrictions, TLS, and safe output.
It contacts no real provider.

## Clean-source suite

Run:

```sh
./scripts/clean-public-source_test.sh
```

The suite exports tracked and non-ignored files to a private temporary directory.
It excludes Git objects, build output, local environment files, and ignored state.
It includes tracked maintainer metadata, including Beads records, by project
publication policy. It preserves safe file modes and internal symbolic links.
It keeps deleted tracked files absent.

The suite runs Go, documentation, Task, release-tag, and release-artifact tests.
It proves that empty environment examples make Compose fail closed.
It verifies release checksums, notices, and empty public secret examples.

## Infrastructure suite

Run:

```sh
./scripts/public-infrastructure_test.sh
```

The suite requires Docker, Kind, `kubectl`, `kubeconform`, and Go 1.26.6.
It builds `index-01-hook:local` from the current tree.
It validates Caddy with no network and a read-only root.
Caddy drops `NET_RAW` and uses `no-new-privileges`.

The suite renders public Kubernetes manifests with synthetic values.
It runs strict `kubeconform` validation for Kubernetes 1.31.0.
It creates one uniquely named Kind cluster with a private kubeconfig.
It loads the local application image into that cluster.

The suite runs strict server dry-run validation for all eight manifests.
It persists only the Namespace, ServiceAccount, NetworkPolicy, and PVC.
It never applies the application Deployment, Service, Ingress, or Secrets.

A restricted probe and maintenance Pod use UID and GID `65532`.
The suite proves SQLite initialization, backup access, and confirmed purge access.
It contacts no provider or production cluster.

## Pinned validation images

- Caddy: `docker.io/library/caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d`
- Kind node: `docker.io/kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5`
- Application: a unique local validation tag built from the current tree

## External blockers

These suites prove local source and disposable infrastructure behavior only.
They do not prove hosted CI, history cleanup, or public source publication.
They do not prove an anonymous GitHub Container Registry pull.
They do not prove published signatures or attestations.
They do not run live provider checks or validate a production cluster.

Complete each external check with separate approval.
Record all results before the first public release.
