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
- `main` → #33 → [#35](https://github.com/klauern/index-01-hook/pull/35) → [#36](https://github.com/klauern/index-01-hook/pull/36) (review corrections) → [#37](https://github.com/klauern/index-01-hook/pull/37) (boundary inventory) → [#38](https://github.com/klauern/index-01-hook/pull/38) (core contracts) → [#39](https://github.com/klauern/index-01-hook/pull/39) (evidence writes and aggregate reads).
- `main` → #26, #27, #31, and #32 independently.

Review each child relative to its immediate base. Review the cumulative module stack with SQLite 1.60.1 and `x/sys` 0.49.0.
The module and tooling stacks can progress independently. Later application slices can branch from either required head before its merge.
Do not make an application slice depend on both stacks unless that slice requires both changes.

When a parent merges, inspect the actual merge result before retargeting a child to `main`.
A squash merge can change ancestry. Do not assume GitHub will present the same child diff after retargeting.
Use a new replacement head if that preserves published history and a small diff.
Keep the previous pull request and branch available as review context.
No pull request merge is part of this maintenance workflow.

## Baseline gates

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


## Source review state

CodeRabbit skipped draft review on #33, #34, and #35. A successful skip status is not completed review.
Independent local source review found three P2 defects in #33. The original parent heads remain unchanged.
[PR #36](https://github.com/klauern/index-01-hook/pull/36) corrects the defects on the cumulative tooling stack.
Its reviewed code revision is `276496bc1526d2ce266d86fef968781ed7db0a29`.
Use the complete #33 → #35 → #36 stack for acceptance. Do not describe #33 alone as corrected.
PR #34 had no concrete finding within its focused module-diff review.
See [the review evidence](first-slice-review.md) for scope, reproductions, corrections, test evidence, and limits.

## Boundary inventory continuation

[PR #37](https://github.com/klauern/index-01-hook/pull/37) starts from #36 at `1f8548f`.
Its reviewed implementation is `12b34b4`. It inventories the complete tooling stack and the committed application baseline.
The slice assigns 91 surfaces, 11 executables, six runtime contracts, and 16 root commands.
Five independent local reviewers inspected the slice. Two reviewers inspected corrections to three guard defects.
The corrected slice passed all six offline gates, focused regression tests, ShellCheck, and actionlint.
The next contract slice can use #37 when it requires this inventory. Keep unrelated module changes independent.
Hosted CI remains a separate gate. Check the actual PR head before accepting or continuing the stack.

## October 17 contract continuation

[PR #38](https://github.com/klauern/index-01-hook/pull/38) starts from #37 at `72217cd`.
The reviewed core specification is `59a9636`. No parent merged during this session.
ADR-0002 defines neutral signatures, identity, stage metadata, safe outcomes, and atomic transition ownership.
Fifteen invariant records reference existing runnable regression tests. The compilation check exercises typed route selection.
Five independent reviewers found three substantive gaps. Three reviewers inspected corrections with no remaining concrete findings in scope.
The corrected cumulative stack passed full uncached Go tests, race tests, vet, Task checks, offline evaluation, and build.
Incremental specification, negative guard, invariant reference, inventory, and Markdown checks passed.
A unique container image passed synthetic volume, backup, and restore integration checks.
Hosted CI remains a separate gate. The parent security baseline has 11 standard-library findings and skipped secret scans.
`index-01-hook-b7y.1.2` remains open for the secondary contracts listed in ADR-0002.
Next: specify lease-checked active-verification evidence and typed redacted reads on this child when required.
No claim of completed extraction or completion on `main` follows from this specification.

## October 18 evidence and read continuation

[PR #39](https://github.com/klauern/index-01-hook/pull/39) starts from #38 at `7dd99c7`.
Its reviewed implementation is `a6c4970`. No parent merged and no new human review finding appeared on #38.
The slice adds the active verification evidence write and typed aggregate read specification.
Real SQLite tests check lease lifecycle, evidence preservation, rejected completion, and read-only aggregate redaction.
The registry has 18 invariant entries. Typed consumers compile without transport or SQL imports.
Five independent local reviewers found no concrete defects. Two checked final typed-reason and failed-transition refinements.
Incremental focused tests and the cumulative six offline gates passed. Synthetic container volume/backup/restore checks also passed.
ShellCheck, actionlint, and diff checks passed. Hosted CI remains a separate gate.
The primary checkout is clean at local `77b7594`. Its source-fix and documentation commits remain independent and excluded.
Health `3411659`, reconciliation `d0ec0f8`, and export fixture `a0c99d1` require separate baseline review before package extraction.
Continue `.1.2` with typed recording/dashboard reads and exact-ID retry/retention contracts.
Keep the issue and parent epic open. This stack is not completion on `main`.
