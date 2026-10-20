# Recover stacked PRs after parent merges

Date: 2026-10-20. Beads `index-01-hook-b7y.25.9` records the repair.

## Observed cause

The user merged #40 and #33 through squash commit `74af6da2b0ab7a9f96d5df9f0b20cad2d45a45c6`.
The user rebased #35 onto that commit and merged it through `bc70071b17b847e5841adada64921d9ea668e0be`.
The fetched #35 head changed from local `d0df228` to remote `9a1da1c` before this repair.
That external update remains preserved. This repair does not rewrite either history.

GitHub retargeted #36 to main. Its old ancestry produced conflicts in two workflow documents.
The conflict resolution keeps #36's expanded review evidence and precise validation instructions.
No runtime source conflict occurred at #36.

## Non-destructive repair

Hold the common-directory session lock through inspection, validation, metadata changes, and publication.
Create a dedicated integration worktree from each existing open PR head.
Merge current main into each open root. Merge each repaired parent into its child.
Preserve the old PR head as an ancestor of the new head. Preserve every published contribution.
Inspect source differences before accepting a resolution. Run incremental and cumulative regressions on the real combined trees.
Publish with ordinary Git push to the existing branch names. Use an atomic push when publishing the complete set.
Do not force-push, rebase published branches, delete PRs, or merge a PR as part of this repair.
Keep all unrelated worktrees and the independent primary checkout unchanged.

The active order is `main` → #36 → #37 → #38 → #39 → #43 → #44.
The intended module order is `main` → #29 → #34.
Actual native stack42 still uses `codex/pr-readiness-baseline-20261018` → #29 → #34.
The four action PRs #26, #27, #31, and #32 remain independent roots on main.
Native stack41 keeps its merged #40/#33/#35 entries as history. Its first open PR is #36.
Native stack42 keeps #29/#34 and its existing root base until approval.

## Supported stack metadata maintenance

This repair inspected official `gh-stack 0.2.1` help and its installed-version source.
The normal sync path can rebase and force-push. Do not use that path under the present authorization.
The `unstack --local` path only removes the selected local catalog entry.
It does not contact GitHub, delete a Git branch, or rewrite a commit.
This exact local-only operation is an exception to the earlier blanket prohibition on `unstack`.
Default or remote `unstack` remains outside this repair.

1. Save the existing catalog as audit evidence outside the Git common directory.
2. In each owned stack worktree, use `GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack unstack --local`.
3. From a clean untracked integration branch, re-adopt the existing open branches with `gh stack init --base main`.
4. The PR-number-only module base update failed with HTTP 422. Do not repeat that request without new relevant evidence.
5. Keep native stack41's closed prefix. Its first open PR already targets main after the user's merge.
6. Use `gh stack link --remote origin 41 36 37 38 39 43 44` to verify existing membership without dropping history.
7. Retarget independent action PRs with the standard PR base command. Native singleton stacks do not exist.
8. Verify every local trunk, live PR base, head, ancestor edge, ready state, and final-head check.

The local catalog tracks only the open branches after re-adoption.
Keep the module catalog on its actual #40 trunk while the remote base change remains blocked.
The remote stack can retain merged history. Compare immediate parents among open members when checking current bases.
Do not hand-edit the catalog or invent dependencies to obtain a native singleton.

## Acceptance and remaining review limits

Beads records exact before/after heads, test results, final hosted checks, and any remaining blockers.
Final acceptance requires terminal green checks and ready status for every remaining PR.
Old-head source review does not become a new full review after an integration merge.
Record source-tree equivalence and local conflict assessment separately from any completed bot pass or skip.
Dependabot author entitlement remains a separate hold. Do not retry it without verified free entitlement evidence.
The #39 documentation measurement remains a diagnostic until new checker evidence establishes its disposition.
The old #40 measurement is historical after the user merged that PR.
No queue pause, scheduler change, paid review, deployment, credential change, or security-setting change is part of this repair.


## Blocked module metadata operation

The native stack API rejects the existing #29 base change with `HTTP 422: PullRequest.base is invalid`.
The request failed before and after publication of the additive repaired head.
The inspected CLI has no supported in-place native stack-base update.
The upstream limitation is recorded in [gh-stack issue 382](https://github.com/github/gh-stack/issues/382).
Native stack42 contains only open #29/#34. Neither PR has automatic merge enabled.
The proposed operation was `gh stack unstack 42`, retarget #29 to main, then relink #29/#34.
Automatic approval review rejected that operation because remote unstack is prohibited and stack metadata must be preserved.
The rejected operation did not execute. Native stack42 remains unchanged.
Explicit approval is required before replacing only that open stack relationship.
The replacement would change its native stack number. PR numbers, commits, comments, and review discussions would remain intact.
Do not unstack stack41. It contains merged history.
No destructive Git history rewrite is required for the published branch repairs.
