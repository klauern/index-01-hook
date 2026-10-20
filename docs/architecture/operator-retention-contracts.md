# Operator retry and retention contract evidence

Date: 2026-10-20.
Scope: Beads `index-01-hook-b7y.25.8`, continuing contract task `.1.2` under architecture sub-epic `.25`.
Immediate prerequisite: [PR #43](https://github.com/klauern/index-01-hook/pull/43) at `67712f4a670947e52ba9b516041cf072d2c368b1`.
Branch: `codex/operator-retention-contracts-20261020`.

## Delivered criteria

ADR-0002 specifies neutral manual-retry and retention service and repository signatures.
Repository methods receive explicit time. Application policy obtains that time from its clock.
Recording retention uses a strict cutoff; evidence expiry uses an inclusive deadline.
The compilation guard exercises typed IDs, service receipts, and both time boundaries without SQL or transport imports.
The invariant registry now has 22 entries.
Production adapter extraction and CLI conversion remain separate tasks.

SQLite state-matrix regressions cover all eight recording states and all seven delivery states.
Only `blocked_auth`, `needs_review`, and `dead_letter` permit manual retry.
The tests check invalid IDs, missing IDs, cancellation, exact-row isolation, completion metadata, and current next-attempt time.
They preserve total attempts, attempt history, completed siblings, remote identity, and original evidence rows.
Synthetic historical counters and terminal metadata are seeded explicitly in SQLite fixtures.
The tested retry methods execute real SQL and real workflow triggers.

A worker integration test uses SQLite and a scripted delivery provider.
After an ambiguous write and manual retry, the worker reconciles the existing item without a create call.
The completed sibling remains unchanged. Total attempts increase, original evidence expiry remains fixed, and completion erases the operational transcript.
This is scripted provider evidence. It does not call TickTick or establish live provider acceptance.

## Chronological retention correction

The previous query compares UTC RFC3339Nano timestamps as raw text.
Go omits zero fractions, so `...00Z` sorts after `...00.000000001Z` despite representing an earlier instant.
That order can delete a recent row or retain an expired row near a cutoff.
Real SQLite regressions failed before the correction for receipt, extraction-update, and delivery-update boundaries in both directions.
The strict-cutoff and independent-evidence regression also failed before the correction.

The corrected query removes the trailing UTC suffix from both operands during comparison.
The existing writers emit canonical UTC timestamps. Removing the suffix keeps optional fractions in chronological text order.
The comparison retains nanosecond precision and avoids floating-point date conversion.
Stored timestamps, schema, retention duration, confirmation requirements, queue-state guards, and evidence deadlines remain unchanged.
The delete remains one atomic SQL statement.

The regression matrix covers older, newer, whole-second equality, and fractional equality for all three timestamp guards.
The exact cutoff retains the row. A later cutoff can delete otherwise eligible inactive work.
A repeated purge returns zero. Operational cascades remove queue rows while independently retained evaluation evidence survives.
At its original expiry instant, evidence purge deletes that evidence.
Existing retention tests continue to protect active work, recent terminal updates, and duplicate receipts.

## Source assessment

Local author inspection checked the retry predicates, workflow triggers, claim counters, reconciliation dispatch, cascade ownership, and timestamp writers.
It checked source separately from test results.
The correction applies only to retention comparisons. It adds no new product retention policy or verification authority.
The complete child stack contains the correction. The unchanged parent PR heads retain their earlier source.
Local inspection, automated source review, hosted CI, and eventual verification on `main` remain separate evidence.

## Validation

Use `GOTOOLCHAIN=go1.26.9` for local Go commands.
Normal Go dependency downloads occurred during the first focused run.

| Command or check | Result |
| --- | --- |
| New retention regressions before the source correction | Expected failure: both chronological directions across three timestamp guards, plus strict cutoff. |
| `go test -count=1 -run 'Test(ApplicationContract\|ApplicationInvariant\|ManualRetryContract\|RetentionContract)' .` | Pass after correction and final equality cases. |
| `go test -race -count=1 -run 'Test(ManualRetryContract\|RetentionContract)' .` | Pass before the added equality cases; final full race gate covers those cases. |
| `task --exit-code validate-offline` | Pass: uncached Go tests, race tests, vet, Task checks, offline evaluation, and build. |
| `shellcheck scripts/*.sh` | Pass. |
| `actionlint .github/workflows/*.yml` | Pass. |
| `govulncheck ./...` | Pass. |
| `gitleaks git --redact --no-banner --exit-code 1 --log-opts=--all .` | Pass. |
| `gitleaks dir --redact --no-banner --exit-code 1 .` | Pass. |
| `./scripts/ci-manifests.sh` | Pass. |
| Pinned Go 1.26.9 Docker build, tag `index01-operator-contracts:20261020` | Pass. |
| `INDEX01_TEST_IMAGE=index01-operator-contracts:20261020 ./scripts/compose-volume_test.sh` | Pass: synthetic volume, backup, and restore. |

Five disposable source controls fail in the intended named tests.
The controls restore the old retention query, include cutoff equality, reset total attempts, remove ambiguous classification, or loosen the exact ID guard.
The first control runner matched no tests; its guard rejected that run.
The corrected controls also prompted independent equality cases for each timestamp guard.
Only the verified named-test failures count as control evidence.
No shared worktree or published branch was mutated for those controls.

Live model checks remain conditionally skipped.
No live provider call, private calibration, deployed end-to-end test, or cluster test ran.
The full offline suite includes synthetic HTTP, webhook, workflow, replay, and SQLite integration checks.
Hosted checks and automated review must be verified on the final published head.

## Stack and resumption

The compiled common-directory lock covers issue updates, code changes, validation, commits, pushes, and PR work.
The isolated worktree is `index-01-hook-operator-contracts` under the delegated task workspace.
Installed `gh-stack 0.2.1` version and `add`/`link` help were inspected.
`gh stack add codex/operator-retention-contracts-20261020` adopted the isolated branch from #43's owner worktree.
Use normal Git push and PR-number-only `gh stack link` to append the created PR to native stack41.
Verify immediate bases and actual ancestry after registration. Do not use commands that force-push or rewrite published history.

The required order is #40 → #33 → #35 → #36 → #37 → #38 → #39 → #43 → this child.
The module stack #29 → #34 remains above #40. Four action PRs remain independent above #40.
The original fourteen PR heads, discussions, and contributions remain preserved.
The bounded older CodeRabbit queue stays separate. Its #39/#40 diagnostics and Dependabot entitlement holds remain unresolved.
Do not spend allowance on those unchanged blockers.

Next contract work covers observation/export and backup/runtime lifecycle, then adapter conversion and package extraction.
All parent migration epics remain open until their full criteria pass and their scope is verified on `main`.
Calibration still requires its human-authored corpus and separately authorized private/live evidence.
Dashboard tool evaluations remain separate from this behavior correction.
