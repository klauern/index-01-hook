# Recover module upgrades after a stack merges to a former base

Beads `index-01-hook-b7y.25.11` records this recovery.
PRs [#29](https://github.com/klauern/index-01-hook/pull/29) and [#34](https://github.com/klauern/index-01-hook/pull/34) merged through commit `aecc6aec5b0caa34d4c95ab508374989da00cb2e`.
Their native stack42 base remained `codex/pr-readiness-baseline-20261018`, the former #40 branch.
The merge updated that branch. The dependency changes remained absent from main.

This recovery starts from current main `0174e3c817916b02264425f81bc17ffbd5b68dc9`.
An additive integration preserves the merged module history and all existing main contributions.
The implementation delta contains only `go.mod` and `go.sum`.
Those files exactly match the merged module commit.

| Module | Main before recovery | Approved merged version |
|---|---|---|
| modernc.org/sqlite | v1.57.0 | v1.60.1 |
| golang.org/x/sys | v0.47.0 | v0.49.0 |
| modernc.org/libc | v1.74.4 | v1.77.1 |
| modernc.org/memory | v1.11.0 | v1.12.1 |

The recovery PR targets main independently. Existing action PRs are separate changes.
No new native stack relationship is required for this independent PR.
Keep native stack42 and its merged review history intact.
The earlier open-stack replacement proposal is obsolete after its members merged.
Do not remote-unstack or recreate that historical stack.

## Repeatable verification

1. Acquire the shared session lock. Read Beads and current remote heads.
2. Check open PRs and main for an existing recovery. Avoid a duplicate PR.
3. Create an isolated worktree from current main.
4. Integrate the approved merged module commit additively.
5. Verify the implementation delta contains only go.mod and go.sum.
6. Verify every unchanged source, workflow, schema, test, and policy file matches main.
7. Run full uncached unit tests, race tests, vet, Task checks, offline evaluation, and build.
8. Run storage migration, SQLite pragma, retry, retention, fencing, and backup regression tests.
9. Run lint, module verification, vulnerability checks, secret scans, and synthetic container ingest, backup, and restore checks.
10. Verify an old-version database remains usable after opening it with the new SQLite module.
11. Push normally and create the independent recovery PR to main.
12. Monitor the exact final head until every configured hosted check passes. Keep the PR ready.

Record exact heads, commands, results, live-provider skips, and review limits in Beads.
Preserve unrelated worktrees and all published contributions. No force-push or PR merge is part of this recovery.
