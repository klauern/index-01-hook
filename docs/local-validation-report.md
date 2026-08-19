# Local public validation report

Validation ran on 2026-08-19.
The host used macOS with a Linux arm64 Docker engine.
No real provider, production cluster, public repository, or release registry was used.

## Tool evidence

- Go: `go1.26.6 darwin/arm64`
- Docker client and server: `29.4.0`
- Kind: `v0.32.0`
- Kubernetes client: `v1.36.3`
- Kubeconform module: `v0.8.0`
- Actionlint: `v1.7.12`
- Govulncheck: `v1.7.0`
- Gitleaks module: `v8.30.1`

The validation used these pinned images:

- Caddy: `docker.io/library/caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d`
- Kind node: `docker.io/kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5`
- Go builder: `docker.io/library/golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6`

## Passed local suites

- Clean public source export
- Empty environment fail-closed check
- Documentation and relative-link checks
- Go tests, race tests, and vet
- Task shell test suite
- Docker Compose configuration and lifecycle tests
- Non-root fresh-volume and incompatible-volume tests
- Synthetic HTTPS DeepSeek and TickTick end-to-end flow
- Provider-free webhook receipt and duplicate behavior
- Caddy route, TLS, and security-header checks
- Public and private Kubeconform checks
- Disposable Kubernetes server dry-run for all eight manifests
- Restricted UID and GID `65532` SQLite write through the PVC
- Maintenance Pod backup and confirmed purge access
- Four-target release binaries and deterministic checksums
- Generated third-party license report and source SPDX SBOM
- Actionlint, ShellCheck, Govulncheck, and Gitleaks history and directory scans

The disposable Kubernetes check rejected the original empty ingress peer.
The portable NetworkPolicy now omits `from` to allow routed sources on port `8080`.
The corrected policy passed Kubernetes server validation.

All disposable Kind clusters, E2E containers, networks, and volumes were removed.
No matching validation cluster or container remained after cleanup.

## External blockers

The following checks are not complete:

- [ ] Approve and perform the Git history rewrite.
- [ ] Run a second full history and directory secret scan after the rewrite.
- [ ] Configure and publish the public GitHub repository.
- [ ] Run hosted GitHub Actions CI on the published commit.
- [ ] Configure protected tags and the `public-release` environment.
- [ ] Run the first gated public release.
- [ ] Confirm public GHCR package visibility and anonymous digest pull.
- [ ] Verify published Cosign signatures and GitHub attestations.
- [ ] Verify the published Release assets, notices, SBOM, and checksums.
- [ ] Run approved live DeepSeek, TickTick, and Index 01 validation.
- [ ] Run an approved external HTTPS deployment check.

Do not call the project publicly released until every required blocker is complete.
