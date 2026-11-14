# Code Health refactor evidence

## Scope

This branch contains a behavior-preserving refactor toward Code Health 10 in project-owned source files.
Beads epic `index-01-hook-a5h` tracks the complete goal.
This measurement does not establish completion of that epic or the architecture migration.

The base is `2613f6b58b2770707c95309ab5ad942ddf932700`.
The branch is `refactor/code-health-10-20261112`.
The changes are not committed, published, or merged at this checkpoint.

## Measurement

CodeScene MCP 1.5.20 and embedded CLI 1.0.58 measured all 123 project-owned Go and JavaScript files.
The measurement used default rules. No thresholds, findings, or checks were suppressed.

- 47 files score 10.
- 74 files remain below 10.
- Two declaration files have no numeric score.
- All 29 changed or new source files score 10.

Third-party `dashboard_static/htmx.min.js` is not part of the project-owned target.
CodeScene does not measure the shell source in this audit.
Unsupported files are not counted as passing.

The [measurement record](code-health/2026-11-12-measurement.json) includes every source hash and score.
Compare the hashes before reusing this evidence.

## Implemented changes

- `index-01-hook-a5h.1`: separate corpus import, validation, coverage, and CLI responsibilities.
- `index-01-hook-a5h.2`: separate trial execution, report aggregation, response validation, and threshold selection.
- `index-01-hook-a5h.4`: separate fixture validation and contributor-test assertions.
- `index-01-hook-b7y.20`: split configuration stages and provider validation without changing runtime policy.

The refactor preserves validation order, error strings, private output handling, and disabled production verification.
Corpus prose, thresholds, provider budgets, and security gates remain unchanged.
The production work-session lock is unchanged.
The configuration tests now report individual fields instead of printing a complete configuration.

## Validation evidence

Focused tests, race tests, vet, ShellCheck, actionlint, and whitespace checks pass.
Independent source reviews found no actionable regressions in the changed slices.
The final `task --exit-code validate-offline` run passed all six gates.
Those gates include uncached Go tests, race tests, vet, Task checks, offline evaluation, and a temporary build.

Differential checks compare original and refactored behavior:

- 49,536 verification-corpus mutations preserve validation results after normalizing existing map-order diagnostics.
- 100 randomized threshold reports are byte-identical.
- Five synthetic provider scenarios preserve calibration reports after normalizing timing fields.
- Spike candidate output is byte-identical.
- Original configuration passes the new error-precedence tests.

Disposable negative controls fail the intended assertions for configuration order, defaults, private output, and sibling conflicts.
Contributor controls retain link, environment, CI, ownership, and fixture checks.
These tests use synthetic data. They do not establish live provider accuracy.

The pinned Docker build and synthetic volume/SQLite-restore runtime test pass.
The local source secret scan passes with no leaks.
The initial vulnerability scanner could not parse the newer local Go 1.27 toolchain.
A second scan used the repository-compatible Go 1.26.10 toolchain.
That scan reports `GO-2026-6859` in the standard library. Go 1.26.11 fixes the finding.
The repository still pins Go 1.26.10, so the security gate does not pass.
No live provider tests, deployment, credential changes, or release actions ran.
Hosted checks have not run for this uncommitted branch.

## Tracking limitation

The session claimed the scoped Beads records before editing.
A later Beads read was blocked for unexpected network egress despite the documented telemetry-off environment option.
No further Beads calls were made after that denial.
The records remain open. Measurement alone does not satisfy all acceptance criteria.
