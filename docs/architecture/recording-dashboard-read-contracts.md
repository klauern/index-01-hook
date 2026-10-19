# Recording and dashboard read contract evidence

Date: 2026-10-19.
Scope: Beads `index-01-hook-b7y.25.7`, continuing `index-01-hook-b7y.1.2` under architecture sub-epic `.25`.
The immediate prerequisite is [PR #39](https://github.com/klauern/index-01-hook/pull/39) at `e5314a703ba1eb56528876cb5b3e437bc7d48074`.
Branch: `codex/recording-dashboard-contracts-20261019`.

## Acceptance evidence

ADR-0002 now specifies private recording status and separate dashboard list/detail ports.
The signatures contain no SQL, HTTP, URL, or concrete store types.
Explicit lookup results distinguish storage absence from storage errors.
The compilation test exercises all three read methods through neutral consumers.
Production wiring and package extraction remain future work.

`TestDashboardReadContractFiltersPaginationAndDetail` uses real SQLite storage and synthetic receipts.
It checks conjunctive exact filters, parameterization, descending ID order, total counts, and two-page navigation.
It also checks empty pages, query defaults, legacy status precedence, and workflow-state selection.
Detail assertions cover item order, completed siblings, remote identity, Unicode character counts, and redaction.
Audio-only receipts have dashboard detail but no extraction-job status.
GET and HEAD retain missing-record responses. Storage errors retain the safe 500 response.
The read-only connection rejects writes. Reads preserve `total_changes()` and database bytes.

`TestDashboardReadContractRejectsUnsafeDatabaseInputs` checks missing files, incompatible schemas, directories, and symlinks to a valid database.
Rejected inputs do not create or migrate a database.
The existing dashboard tests retain HTML redaction, aggregate evidence privacy, headers, host restrictions, and method restrictions.
The invariant registry now has 19 entries.

## Source assessment

Source assessment checked `RecordingStatus`, dashboard SQL, query parsing, rendering, and database opening independently of test results.
The contracts preserve the distinction between extraction workflow states and lease states.
The dashboard model omits private fingerprints, markers, transcript text, filenames, and verification detail.
The operator model retains those private status fields where existing consumers require them.
SQLite absence will map to explicit lookup results during extraction. No extracted adapter parity is claimed here.
No production source change was needed for the specified behavior.
This assessment is local author inspection. Automated source review remains separate evidence.

## Validation

Use patched Go with `GOTOOLCHAIN=go1.26.9`.

| Command | Result |
| --- | --- |
| `go test -count=1 -run 'Test(ApplicationContract\|ApplicationInvariant\|DashboardReadContract\|DashboardReadOnly\|DashboardJev)' .` | Pass. |
| `go test -race -count=1 -run TestDashboardReadContract .` | Pass. |
| `task --exit-code validate-offline` | Pass: uncached Go tests, race tests, vet, Task checks, offline evaluation, and build. |
| `shellcheck scripts/*.sh` | Pass. |
| `actionlint .github/workflows/*.yml` | Pass. |
| `govulncheck ./...` | Pass. |
| `gitleaks git --redact --no-banner --exit-code 1 --log-opts=--all .` | Pass. |
| `gitleaks dir --redact --no-banner --exit-code 1 .` | Pass. |
| `./scripts/ci-manifests.sh` | Pass. |
| Pinned Go 1.26.9 Docker build, tag `index01-daily-read-contracts:20261019` | Pass. |
| `INDEX01_TEST_IMAGE=index01-daily-read-contracts:20261019 ./scripts/compose-volume_test.sh` | Pass: synthetic volume, backup, and restore. |

Disposable source controls changed one behavior at a time.
The new regressions failed for OR filters, symlink following, writable database mode, and reversed detail order.
The shared checkout and published branches were not changed for these controls.
The first focused attempt incorrectly expected transcript erasure with an incomplete sibling.
The corrected assertion requires the existing 30-character evidence value, including the Unicode character.
Subsequent focused checks passed.

Live model tests remain conditionally skipped. No provider calls, private calibration, deployment end-to-end tests, or cluster tests ran.
Synthetic HTTP, webhook, workflow, replay, and SQLite integration tests run within the offline suite.
Hosted checks and automated review must be inspected on the final published head.
Passing offline checks does not establish model accuracy, adapter extraction, or completion on `main`.

## Stack and resumption

Hold the common-directory session lock through Beads updates and publication.
The clean child worktree is `index-01-hook-dashboard-contracts` under the delegated task workspace.
The installed official `gh-stack 0.2.1` was inspected before use, including its `add` implementation and command help.
`gh stack add codex/recording-dashboard-contracts-20261019` adopted the isolated branch from the #39 owner worktree.
No staging flags or checkout transfer were used.
Use normal Git push. Append the created PR number to native stack41 with `gh stack link`.
Verify local membership, actual ancestry, and live immediate-parent bases after publication.
Do not use stack push, submit, sync, rebase, or merge under this authorization.

The required order is #40 → #33 → #35 → #36 → #37 → #38 → #39 → this child.
The #29 → #34 stack and four action PRs retain their existing bases and identities.
No parent branch requires this child for its own validation.
Keep #39 and #40 documentation diagnostics unresolved unless new provider evidence resolves them.
Keep Dependabot author-entitlement holds and #34's review dependency explicit.
Do not spend review capacity on those unchanged blockers.

Resume from the branch and exact published revision recorded in Beads.
Inspect local edits, remote heads, reviews, and checks before adding another layer.
Next: specify exact-ID retry and retention contracts, including allowed states, counts, and original evidence expiry.
The parent contract task and all migration epics remain open.
