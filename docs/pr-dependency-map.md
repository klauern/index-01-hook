# Pull request dependency map

This map records the inspected pull requests and safe review order on 2026-10-16.
Beads remains the task tracker. Read current pull request heads and bases before changing a stack.
Review and merge timing do not stop implementation. Continue from an unmerged prerequisite when its code is required.

## Inspected changes

| Pull request | Inspected head | Changed surface | Dependency decision |
| --- | --- | --- | --- |
| [#26](https://github.com/klauern/index-01-hook/pull/26) | `81331f1` | Build-and-push action pin in `release.yml` | Independent. Keep `main` as base. |
| [#27](https://github.com/klauern/index-01-hook/pull/27) | `0c1f9d8` | Buildx action pin in `release.yml` | Independent. Keep `main` as base. |
| [#29](https://github.com/klauern/index-01-hook/pull/29) | `077adf4` | SQLite 1.60.1, libc, memory, and `x/sys` 0.48.0 | Module stack base. Keep `main` as base. |
| [#30](https://github.com/klauern/index-01-hook/pull/30) | `b63a6d2` | `x/sys` 0.49.0 and checksums | Overlaps #29. Preserve its branch and discussion. Superseded by [#34](https://github.com/klauern/index-01-hook/pull/34), an incremental child of #29. |
| [#31](https://github.com/klauern/index-01-hook/pull/31) | `b2a5074` | Upload-artifact action pin in `release.yml` | Independent. Keep `main` as base. |
| [#32](https://github.com/klauern/index-01-hook/pull/32) | `d865fad` | Download-artifact action pin in `release.yml` | Independent. Keep `main` as base. |
| [#33](https://github.com/klauern/index-01-hook/pull/33) | `cc0dda3` | Session lock, offline validation, and workflow documentation | Tooling stack base. Keep `main` as base. |

The four action pull requests change separate lines. Their shared filename does not establish a dependency.
No runtime compatibility claim follows from that observation. Their action versions still need the normal tests and review.
Their existing test failures require pin-contract review before acceptance. Do not relax checks without reviewing the new action contract.

The module overlap is concrete. PR #29 replaces `x/sys` 0.47.0 with 0.48.0.
PR #30 replaces the same requirement and checksum entries with 0.49.0.
Retargeting #30 to #29 without changing its ancestry would expose a cumulative or misleading diff.
Replacing the child preserves both upgrades without force-pushing a published branch.
PR #30 had no submitted human reviews during inspection. Its bot discussion remains available through the original link.

## Review and merge order

The workflow-policy child is [#35](https://github.com/klauern/index-01-hook/pull/35), based on `codex/workflow-baseline-20261016`.
Its initial documentation commit is `d103f7f`.

The new module child is [#34](https://github.com/klauern/index-01-hook/pull/34), head `883d306`.
Its base is `dependabot/go_modules/modernc.org/sqlite-1.60.1` at `077adf4`.
Its diff changes one module requirement and two checksum entries.
The original #30 branch remains unchanged at `b63a6d2`.

The dependency edges are:

- `main` → #29 → #34 (`x/sys` child).
- `main` → #33 → [#35](https://github.com/klauern/index-01-hook/pull/35) (workflow-policy child).
- `main` → #26, #27, #31, and #32 independently.

Review each child relative to its immediate base. Review the cumulative module stack with SQLite 1.60.1 and `x/sys` 0.49.0.
The module and tooling stacks can progress independently. Later application slices can branch from either required head before its merge.
Do not make an application slice depend on both stacks unless that slice requires both changes.

When a parent merges, inspect the actual merge result before retargeting a child to `main`.
A squash merge can change ancestry. Do not assume GitHub will present the same child diff after retargeting.
Use a new replacement head if that preserves published history and a small diff.
Keep the previous pull request and branch available as review context.
No pull request merge is part of this maintenance workflow.

## Original baseline gates (historical)

PR #33's original head completed both CI runs:
[branch run](https://github.com/klauern/index-01-hook/actions/runs/38837309392) and
[pull request run](https://github.com/klauern/index-01-hook/actions/runs/38837313734).
Tests, container checks, and manifest checks passed in both runs. CodeQL passed.
Both security jobs failed at govulncheck with 11 Go standard-library vulnerability IDs.
The IDs exactly match the [earlier baseline job](https://github.com/klauern/index-01-hook/actions/runs/38648638258/job/116141664535).
The history and tree secret scans were skipped after govulncheck failed. They remain unverified.
These failures block the affected acceptance gate. They do not require independent implementation to stop.

The public-source export fixture has a separate baseline metadata failure.
The pending primary-checkout work already changes that fixture. Do not duplicate those edits while preparing a stack.
Live provider actions, deployments, credential changes, security changes, and pull request merges remain outside this workflow.


## Stack validation

The #29 → #34 cumulative stack passed uncached full Go tests, race tests, vet, Task checks, offline evaluation, and a native build.
Focused incremental checks passed for database upgrade, backup, restore, and extraction/delivery leases.
A uniquely tagged Docker build and synthetic SQLite volume/backup/restore test also passed.
`go list -m` confirmed SQLite 1.60.1 and `x/sys` 0.49.0.
The incremental diff contains only the `x/sys` requirement and checksum update.

The #33 → #35 cumulative stack passed `scripts/validate-offline.sh`, including all six offline gates.
Focused Markdown, README, and documentation checks passed for the incremental policy change.
Live provider tests remained disabled in both stacks. Hosted checks are separate from these local results.
Keep implementation moving on either tested stack while review and merge decisions remain pending.

## Green-and-ready repair pass

Nick now requires every current and future PR to have source assessment, ready status, and green final-head checks.
The [readiness policy](pr-readiness.md) supersedes the previous temporary tolerance of inherited security failures.
[PR #40](https://github.com/klauern/index-01-hook/pull/40), head `7a3e9c9`, is the shared Go 1.26.9 and export-fixture prerequisite.
Main merge order starts with #40. The independent children are #26, #27, #29, #31, #32, and #33.
Then merge #29 before #34, and #33 before #35 → #36 → #37 → #38 → #39.
No merge is authorized as part of this pass.

The three defects from the new CodeRabbit review of #33 at `cc0dda3` are now corrected at the parent in `12b29bf`.
This additive backport preserves the original PR #36 correction `276496b` and all subsequent review history.
Review each child relative to its actual immediate base after propagation.
The source patch is identical; documentation now requires compiled session invocation, isolated reports, cancellation exits, and the readiness gate.
The original skipped-review statuses remain historical evidence. The new #33 COMMENTED review is a completed review with three findings.
Required checks are `test`, `container`, `manifests`, and `security`. Inspect other checks and review comments as well.
Verify current exact heads and all terminal results through GitHub. Do not infer readiness from old success or skipped statuses.
