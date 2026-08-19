# Deployment reference

Docker Compose is the primary deployment method. Use the [Docker Compose guide](docker-compose.md) for local deployment and routine lifecycle operations.
Use the [advanced Kubernetes guide](kubernetes.md) for portable cluster
resources and Kubernetes lifecycle operations.

This document is a concise packaging and safety-gate reference. It does not claim
cluster validation, provider validation, or public image publication.

## Immutable build metadata

Build each release with explicit version, source commit, and UTC build date:

```sh
export VERSION='v0.1.0'
export COMMIT='0123456789abcdef0123456789abcdef01234567'
export BUILD_DATE='2026-08-12T00:00:00Z'
task build VERSION="$VERSION" COMMIT="$COMMIT" BUILD_DATE="$BUILD_DATE"
```

The build writes a Linux binary and SHA-256 checksum in `dist/`. Build container
images with an immutable Go builder reference:

```sh
export BUILDER_IMAGE='registry.example.org/library/golang@sha256:0000000000000000000000000000000000000000000000000000000000000000'
export IMAGE_TAG='registry.example.org/owner/index-01-hook:v0.1.0'
task image \
  GO_IMAGE="$BUILDER_IMAGE" \
  IMAGE_TAG="$IMAGE_TAG" \
  VERSION="$VERSION" \
  COMMIT="$COMMIT" \
  BUILD_DATE="$BUILD_DATE"
```

Record the final registry digest. Production deployment examples must use the
form `registry.example.org/owner/index-01-hook@sha256:<image-digest>`, not a tag.
Gated release automation exists, but no public image exists until the first
approved release completes. See [the maintainer release guide](../RELEASING.md)
and [the release approval checklist](release-approval.md).

## Release gate
CI has four checks: `test`, `container`, `manifests`, and `security`. The local
equivalents use [Task](https://taskfile.dev/) v3.53.1,
[kubeconform](https://github.com/yannh/kubeconform) v0.8.0,
[govulncheck](https://go.dev/doc/tutorial/govulncheck) v1.7.0,
[gitleaks](https://github.com/gitleaks/gitleaks) v8.30.1, and
[actionlint](https://github.com/rhysd/actionlint) v1.7.12. Install these exact
tools, plus Docker, Docker Compose, and ShellCheck, before you run the gate. Use
the installation method for the operator's system from each official project. The
gate does not install missing tools and does not call providers or deploy resources.

Run the local release checks from the repository root:

```sh
set -eu
go mod download all
GOTOOLCHAIN=local GOPROXY=off go test ./...
GOTOOLCHAIN=local GOPROXY=off go test -race ./...
GOTOOLCHAIN=local GOPROXY=off go vet ./...
GOTOOLCHAIN=local GOPROXY=off task test
docker build --pull=false --no-cache \
  --build-arg GO_IMAGE=docker.io/library/golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 \
  --tag index-01-hook:local .
./scripts/compose-volume_test.sh
./scripts/ci-manifests.sh
shellcheck scripts/*.sh
govulncheck ./...
gitleaks git --redact --no-banner --exit-code 1 --log-opts="--all" .
gitleaks dir --redact --no-banner --exit-code 1 .
actionlint .github/workflows/*.yml
task build VERSION="$VERSION" COMMIT="$COMMIT" BUILD_DATE="$BUILD_DATE"
shasum -a 256 -c dist/index-01-hook-linux-amd64-v0.1.0.sha256
```

Stop when a check fails or a required tool is missing. Do not release an artifact
without versioned build metadata and its checksum.

## Kubernetes package validation

Render the portable Kubernetes package with an immutable image digest, registry
access mode, fully qualified host, IngressClass, and TLS Secret name. Replace
the synthetic values before a live operation:

```sh
export IMAGE_REF='registry.example.org/owner/index-01-hook@sha256:1111111111111111111111111111111111111111111111111111111111111111'
export KUBE_CONTEXT='replace-with-approved-context'
export KUBE_INGRESS_HOST='hook.example.org'
export KUBE_INGRESS_CLASS='standard'
export KUBE_TLS_SECRET='index-01-hook-tls'
task render \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE=public \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET"
```

Run client validation before any cluster change:

```sh
task dry-run \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE=public \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

After explicit approval, run server validation and deployment with the same values:

```sh
task server-dry-run \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE=public \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_CONTEXT="$KUBE_CONTEXT"

task deploy \
  IMAGE_REF="$IMAGE_REF" \
  REGISTRY_ACCESS_MODE=public \
  KUBE_INGRESS_HOST="$KUBE_INGRESS_HOST" \
  KUBE_INGRESS_CLASS="$KUBE_INGRESS_CLASS" \
  KUBE_TLS_SECRET="$KUBE_TLS_SECRET" \
  KUBE_CONTEXT="$KUBE_CONTEXT"
```

Use the Kubernetes guide for Secret creation, storage selection, context checks,
backup, restore, upgrade, rollback, and safe removal.

## Safety gates

Before a live operation, record:

- The source commit, build metadata, and immutable image digest.
- The exact Kubernetes context and namespace.
- The validation results and approved registry mode.
- The verified encrypted backup and SHA-256 checksum.
- The approved operation, target, time window, and rollback identity.

Keep live cluster commands, provider calls, registry access, backup, restore, and
deployment changes outside normal contributor tests. Use synthetic inputs for
contributor checks. Never claim live validation when only local checks ran.
