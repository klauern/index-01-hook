# Health snapshot reader refactor

## Scope and dependency

This slice depends on PR #70 at `3e945fce766155f1bdace880adc65a40c2154456`.
Merge PR #70 before this slice. The child PR targets the parent branch, not main.
The slice extracts ordered worker/provider, intake, and queue queries from `OperationalStatus`.
It preserves SQL, scan mappings, one clock snapshot, public signatures, error order, and zero output on failure.
The boundary inventory assigns the extracted reader to persistence. No architecture check is disabled.

## Code Health

The existing CodeScene MCP 1.5.20 configuration measured project-owned files with default rules.
`health.go` improved from 8.32 to 8.81. The new reader and regression files each score 10.
The [measurement record](code-health/2026-11-14-health-readers.json) contains source hashes and remaining findings.
Complex health reasons, duplicated worker writes, and the existing provider method signature remain separate work.
This slice does not complete module-wide or project-wide CThis slice does not complete modo 1This slice does not cooffline gates on the combined parent and child source.
These include uncached unit tests, race tests, vet, Task checks, offline evaluation, and build.
After splitting one compound test assertion, the focused health, readiness, and inventory race tests passed again.
The new regression suite also passed against unchanged parent source in a disposable archive.
It covers query-error precedence, invalid queue time, zero partial output, one clock read, and TypeSafe latency mapping.
An independent source review found no behavioral defect.
Govulncheck 1.7.0 reported no vulnerabilities. ShellCheck, actionlint, and whitespace checks passed.

The first full attempt found the missing new-file inventory entry. The entry was added and the full rerun passed.
The restricted runtime could not access existing CodeScene authentication; the approved execution route reused the existing sign-in.
No authentication settings, rule thresholds, or check policies changed.
No live provider tests ran. Container tests were not rerun for this read-path slice; the parent passed its pinned build and SQLite restore test.
Hosted child checks remain separate and must pass on the published child head.

## Native stack verification

On November 14, official github/gh-stack 0.2.1 created native stack #72 with PR-number-only linking.
The verified merge order is main, PR #70, then PR #71.
PR #70 targets main at head 3e945fce766155f1bdace880adc65a40c2154456.
PR #71 targets refactor/code-health-10-20261112; its initial source head is 8ef178358b939673fd3dce73e3743e5dfa1a2b64.
GitHub stack metadata and both immediate-parent PR bases matched after registration.
No force push, rebase, merge, or unrelated stack change ran.
The local legacy stack catalog remains untouched; the native remote stack is authoritative for this pair.
