# Boundary inventory

This inventory supports `index-01-hook-b7y.1.1`. It records source ownership before package moves.
The starting behavior map is [Architecture](../architecture.md).
The source baseline is `85f2a46` plus the tooling stack through `1f8548f` in PR #36.
No pending primary-checkout application change is included.

[boundary-inventory.json](boundary-inventory.json) lists every assigned path, executable entry point, and root command.
It is the machine-checked inventory. Beads remains the task tracker.
Ownership identifies the primary migration responsibility. It does not assert that current files have separated concerns.
Exact package names, port signatures, and transaction contracts remain work under `index-01-hook-b7y.1.2` and later tasks.

## Ownership

| Owner | Meaning | Assigned surfaces |
| --- | --- | ---: |
| `application_policy` | Workflow decisions, neutral item/evidence models, date rules, and verification policy. | 6 |
| `provider` | DeepSeek, TickTick, TypeSafe transport, and remote observation translation. | 4 |
| `persistence` | SQLite schema, transactions, retention, evidence storage, backup, and restore. | 16 |
| `inbound_interface` | Webhook, dashboard, operator commands, validation commands, and static dashboard assets. | 8 |
| `runtime` | Configuration, dependency construction, process lifecycle, shutdown, and cleanup. | 4 |
| `tooling` | Corpus processing, calibration commands, release helpers, synthetic fixtures, and executable shell checks. | 53 |

The 91 assigned surfaces include every non-test Go file across build targets, every executable script, and every embedded asset.
Executable test scripts remain tooling surfaces. Go test files are excluded from production ownership.
The synthetic provider executable is tooling; it is not a production provider adapter.
The shared `internal/evalcorpus/types.go` models belong to application policy. Corpus import and scoring helpers remain tooling.

## Mixed files and required splits

These assignments name a primary owner. The following existing responsibilities must remain visible during extraction.

| Source | Primary owner | Responsibilities that cross the current file boundary |
| --- | --- | --- |
| [worker.go](../../worker.go) | Application policy | Polling, leases, retries, shadow scheduling, and execution currently share one file. Runtime owns polling and scheduling. |
| [queue.go](../../queue.go) | Persistence | Durable transitions and validation must keep their transaction boundaries when application ports are extracted. |
| [health.go](../../health.go) | Application policy | Health decisions and telemetry queries share the file. Persistence owns queries; inbound adapters expose the decisions. |
| [typesafe_verification.go](../../typesafe_verification.go) | Application policy | Verification decisions and the concrete TypeSafe adapter share the file. Provider transport and neutral assessment contracts must separate. |
| [evaluation_collection.go](../../evaluation_collection.go) | Provider | Remote observation reads and the polling loop share the file. Runtime owns polling; persistence owns stored observations. |
| [evaluation_evidence.go](../../evaluation_evidence.go) | Persistence | Evidence types and SQL share the file. Extract neutral contracts without breaking atomic capture. |
| [evaluation_export.go](../../evaluation_export.go) | Inbound interface | Command parsing, export construction, and filesystem writes share the file. Keep application export policy separate from file delivery. |
| [dashboard.go](../../dashboard.go) | Inbound interface | HTTP views, read-only database queries, browser launch, and server lifecycle share the file. Preserve read-only access. |
| [operator.go](../../operator.go) | Inbound interface | Parsing, use cases, SQL, and backup/restore filesystem operations share the file. Preserve the public command contract. |
| [config.go](../../config.go) | Runtime | Environment parsing and configuration validation remain at composition until their application-owned rules are identified. |

The pending moved-item reconciliation and health-expiry changes are excluded.
Do not infer that the inventory commits those fixes or accepts them as a migration baseline.
`index-01-hook-b7y.2.1` still owns their source disposition.
The separate architecture documentation commit `74f79ad` remains preserved. This slice inventories the committed source and tooling stack only.

## Runtime entry points

The JSON inventory assigns all 11 Go executable entry points.

| Executable | Entry source | Responsibility |
| --- | --- | --- |
| Root service/operator binary | [main.go](../../main.go) | Runtime construction; dispatch through `executeWithInput`. |
| Synthetic provider fixture | [e2e/provider-fixture/main.go](../../e2e/provider-fixture/main.go) | Tooling HTTP fixture for local end-to-end tests. |
| Corpus builder | [scripts/eval-corpus/main.go](../../scripts/eval-corpus/main.go) | Tooling conversion of private snapshots or ledgers. |
| Release notices | [scripts/release-notices/main.go](../../scripts/release-notices/main.go) | Tooling license report generation. |
| Rename helper | [scripts/rename-no-replace/main.go](../../scripts/rename-no-replace/main.go) | Tooling adapter with Darwin, Linux, and fallback source files. |
| Routing feedback | [scripts/routing-feedback/main.go](../../scripts/routing-feedback/main.go) | Tooling comparison of saved snapshots. |
| TickTick pull | [scripts/ticktick-pull/main.go](../../scripts/ticktick-pull/main.go) | Tooling observation export; live access remains separately gated. |
| TypeSafe calibration | [scripts/typesafe-calibration/main.go](../../scripts/typesafe-calibration/main.go) | Tooling calibration; paid/live calls remain separately gated. |
| TypeSafe spike | [scripts/typesafe-spike/main.go](../../scripts/typesafe-spike/main.go) | Tooling prototype; live calls remain separately gated. |
| TypeSafe thresholds | [scripts/typesafe-thresholds/main.go](../../scripts/typesafe-thresholds/main.go) | Tooling threshold report from saved scores. |
| Work session lock | [scripts/work-session/main.go](../../scripts/work-session/main.go) | Tooling coordination across worktrees of one clone. |

The root command map has 16 commands:
`serve`, `validate-config`, `dashboard`, `version`, `healthcheck`, `maintenance`, `ticktick-projects`,
`evaluation-status`, `evaluation-collect`, `evaluation-export`, `purge-expired`, `restore`,
`status`, `retry-recording`, `retry-delivery`, and `backup`.
No arguments selects `serve`; it is not a separate command.

The inventory also names the webhook HTTP constructor, dashboard driver, worker loop, evaluation collection loop, and operator dispatcher.
[main.go](../../main.go) composes the service, worker, and collection loop.
[webhook.go](../../webhook.go) registers `POST /webhook`, `GET /healthz`, `GET /statusz`, and `GET /readyz`.
[dashboard.go](../../dashboard.go) accepts GET and HEAD for `/`, `/dashboard`, `/status`, `/recordings`, and `/recordings/{id}`.
It also serves `/static/htmx.min.js` and `/static/dashboard.css`.
These routes share the named inbound constructors. Existing HTTP contract tests retain responsibility for route behavior.
Deployment wrappers in [compose.yaml](../../compose.yaml), [Dockerfile](../../Dockerfile), [Taskfile.yml](../../Taskfile.yml), and [deploy](../../deploy) select these commands.
The inventory does not execute deployment wrappers or grant permission to run them.

## Embedded assets

[store.go](../../store.go) embeds all 11 `migrations/*.sql` files. Persistence owns these assets.
[dashboard.go](../../dashboard.go) embeds three `dashboard_static/*` files. The inbound interface owns these assets, including the license notice.
The coverage test discovers these assets from source directives rather than a separate hard-coded filename list.

## Validation contract

Run:

```sh
go test -count=1 -run '^Test(ArchitectureBoundaryInventory|Boundary)' -v .
```

The repository test scans source rather than the Git index. New unstaged source cannot bypass assignment checks.
It includes build-tagged Go files across platforms, executable shebang scripts, current embedded file globs, and root dispatcher commands.
It rejects missing paths, stale paths, duplicate assignments, unknown owners, stale entry functions, and unassigned executable entry points.
Six explicit runtime contracts also require assignments and existing source functions.
Renaming or removing these contracts requires a guard update. New runtime constructors still require source review.
The decoder rejects trailing JSON values and invalid content.
Only root metadata, `dist`, `node_modules`, and `vendor` directories are excluded. Nested source directories remain covered.
Synthetic discovery tests add a new platform file, action source, script, embedded asset, executable, and command.
They verify that missing inventory entries fail validation.

The scanner intentionally supports the current unquoted file-glob embed syntax.
New quoted patterns, embedded directories, `all:` patterns, or symlinks require an explicit discovery update.
Root command discovery follows literal comparisons and switch cases on `args[0]` in `executeWithInput`.
A new dispatch representation requires review of the discovery check as well as the command map.
The test checks completeness and metadata consistency. Source review still checks whether the selected owners are correct.
It does not enforce import direction or demonstrate completed package extraction.
Those acceptance gates remain under `index-01-hook-b7y.21` and the later migration tasks.

## Source review evidence

Five independent local reviewers inspected this slice against parent `1f8548f`.
They reviewed discovery, assignments, mutation coverage, ownership, and scope.
They found two completeness defects and one JSON parsing defect.
The corrections restrict exclusions to root artifact paths, require six runtime contracts, and reject trailing JSON content.
Regression tests reproduce all three defects. Unsupported embed patterns also have rejection tests.
Two reviewers inspected the corrections and reported no remaining concrete findings.
Source review remains separate from test and hosted CI results. No external review service was requested.

## Repeatable continuation

Follow [the recurring workflow](../recurring-work.md) and hold the shared clone lock for the entire session.
Read the current Beads issue and current PR heads before selecting a base.
Preserve the primary checkout and inspect this worktree for local changes before resuming.
Continue from the inventory PR when the next contract slice needs this ownership baseline.
`index-01-hook-b7y.1.2` defines neutral contracts, error categories, identities, stage metadata, and transaction ownership.
Record each invariant owner and its validation. Keep transport and SQL types outside the application contracts.
The parent boundary epic remains open until its own acceptance criteria pass.
