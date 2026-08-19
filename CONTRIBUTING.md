# Contributing

Thank you for helping improve Index 01 Hook.

The public fork and issue workflow becomes available after source publication.
The README release status states when the repository is still private.

## Prerequisites

Install these tools before you work on the project:

- Go 1.26.6 or newer to build and test the service.
- Git to clone the repository and manage branches.
- Task to run commands in `Taskfile.yml`.
- The Docker CLI with the Docker Compose plugin to run static Compose checks.
- Docker Engine to build or run a container image.

The `task test` command requires Task, shell tools, and Docker Compose. A running
Docker daemon is not required for `task test`. The `task test-compose-runtime`
command also requires a running Docker daemon.

## Workflow

Use a fork or a branch in your fork. Do not require maintainer access.

1. Fork `https://github.com/klauern/index-01-hook`.
2. Clone your fork.
3. Create a focused branch from the current default branch.
4. Make the smallest change that solves the problem.
5. Run the checks that apply to the change.
6. Push the branch to your fork.
7. Open a pull request from the fork.

Explain the problem, the change, and the checks in the pull request. Keep one
main change in each pull request.

## Test requirements

Use synthetic inputs in contributor tests. Tests must not require live
DeepSeek, TickTick, Index 01, registry, or Kubernetes actions.

Run focused tests for the changed behavior. Also run these checks:

```sh
go test ./...
go test -race ./...
go vet ./...
task test
```

Run `task test` when Task, the required shell tools, and Docker Compose are
available. Run `task test-compose-runtime` when a Docker daemon is running. If a
check cannot run, state the reason in the pull request.

Contributors must not run live tests unless maintainers approve them first.

## Continuous integration

The CI workflow has four checks: `test`, `container`, `manifests`, and `security`.
Run the local equivalent before you open a pull request.

- `test`: Install Task v3.53.1. Use the hosted runner's Docker Compose and
  ShellCheck. Run `go mod download`.
  Then run tests, race tests, vet, Task checks, and a release-shaped binary build.
- `container`: Build with the pinned Go builder from CI and run `./scripts/compose-volume_test.sh`.
- `manifests`: Install kubeconform v0.8.0. Validate public and private manifests
  with default and explicit storage classes.
- `security`: Run `shellcheck scripts/*.sh`, govulncheck v1.7.0, gitleaks v8.30.1,
  and actionlint v1.7.12. Scan the complete Git history and checked-out tree.

The CI workflow uses synthetic inputs. It does not use provider credentials or cluster access.

Do not put credentials or private payloads in issues, tests, logs, or commits.
Redact provider data, webhook data, and other private values from test output.

## Documentation

Document every configuration or behavior change. Update the relevant README or
public documentation with examples that contain synthetic values only.

## Contribution license

This project uses the MIT License. By submitting a contribution, you provide
the contribution under the MIT License and confirm that you have the right to
do so. The project does not require a copyright assignment.
