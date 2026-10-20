# Native stacked pull requests

Use the official `github/gh-stack` extension for existing stacks and future dependent work.
Beads remains the task authority. GitHub stack membership and local metadata describe dependencies.
Hold the shared clone session lock during all stack operations.

## Installed tool and inspected behavior

The October 19 adoption used GitHub CLI 2.102.0 and pinned `gh-stack 0.2.1`.
Install this inspected version with:

```sh
gh extension install github/gh-stack --pin v0.2.1
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack --version
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack link --help
```

Read command help and inspect a new version before changing this workflow.
The [official source](https://github.com/github/gh-stack/tree/v0.2.1) defines these relevant behaviors:

- `init` adopts existing branches, finds their existing PRs, and records local metadata. It enables Git rerere locally.
- `link` with existing PR numbers creates or updates native GitHub stack membership without pushing branch arguments.
- `link` can correct PR bases. Verify every expected base before invoking it, then compare the resulting graph.
- Local tracking uses the Git common directory's `gh-stack` JSON file. All linked worktrees share it.
- The tool does not create or remove worktrees. It preserves branches checked out in another worktree.
- Stack chains are linear. Independent siblings need separate stacks, with a shared prerequisite used as their trunk.

`gh stack push` 0.2.1 always uses explicit `--force-with-lease` checks.
`submit` and `sync` can also push, rebase, or change PR state.
The current task forbids force pushes and history rewrites. Use normal `git push` and PR-number-only `gh stack link`.
Do not run `gh stack push`, `submit`, `sync`, `rebase`, `modify`, `merge`, or remote `unstack` under this authorization.
For parent-merge recovery only, inspected `unstack --local` may remove local catalog tracking before explicit re-adoption; it preserves Git branches, commits, and GitHub stacks. See [merge recovery](stack-merge-recovery.md).
Do not follow generic tool suggestions that conflict with these limits.

## Historical October 19 registration

After the October 20 parent merges, the active order is `main → #36 → #37 → #38 → #39 → #43 → #44`; stack41 keeps its merged prefix. Stack42 is `main → #29 → #34`; #26/#27/#31/#32 independently target main. The registration below is historical.

## Registered graph

The native GitHub [stack41](https://api.github.com/repos/klauern/index-01-hook/stacks/41) has base `main`:

```text
main → #40 → #33 → #35 → #36 → #37 → #38 → #39
```

The native GitHub [stack42](https://api.github.com/repos/klauern/index-01-hook/stacks/42) has base `codex/pr-readiness-baseline-20261018`, the branch for #40:

```text
#40 prerequisite → #29 → #34
```

The four action PRs #26, #27, #31, and #32 each have the same #40 prerequisite.
Each action branch has its own one-layer local `gh stack` entry. They remain independent on GitHub.
Creating a native stack with `gh stack link` requires at least two PRs.
Append mode takes an existing stack number and one or more PRs. Do not invent dependencies to create native singleton stacks.
All thirteen original PR numbers, contributions, and review discussions remain preserved.
Native stack membership does not establish source review, author entitlement, CI success, or permission to merge.

## Resume and inspect

1. Read repository instructions, `docs/free-oss-reviews.md`, and current Beads state.
2. Hold the compiled shared session lock. Preserve all existing edits and unrelated worktrees.
3. Inspect the extension version and help. Inspect common-directory metadata and any recovery journal.
4. In the relevant owned worktree, run `GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack view --json`.
5. Read native remote membership with `gh api 'repos/klauern/index-01-hook/stacks?per_page=100'`.
6. Compare local stack PR numbers with live PR heads and immediate bases. Check ancestry and ready state.
7. If an operation is pending, inspect its recorded owner. Do not edit or delete recovery metadata.
8. If the current branch already belongs to a stack, use its existing catalog entry. Do not initialize it again.

The active stack depends on the worktree branch. The #40 branch belongs to the main chain and serves as other stacks' trunk.
Use each sibling's owned worktree to inspect that sibling stack.
Never hand-edit the local catalog to force a conflicting definition.

## Register and maintain without rewriting history

The initial remote registration used existing PR numbers:

```sh
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack link --base main --remote origin 40 33 35 36 37 38 39
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack link --base codex/pr-readiness-baseline-20261018 --remote origin 29 34
```

These commands are additive and idempotent for the matching graph. They preserve existing PR bodies and discussions.
Use the exact expected trunk. Omitting `--base` for the module stack would request the wrong bottom base.

The local main chain was adopted from the clean #39 worktree:

```sh
GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack init --base main codex/pr-readiness-baseline-20261018 codex/workflow-baseline-20261016 codex/continuing-stacks-20261016 codex/first-slice-review-fixes-20261016 codex/boundary-inventory-20261016 codex/application-contracts-20261017 codex/verification-read-contracts-20261018
```

The module chain was adopted from its clean #34 worktree with explicit #40 trunk.
Each independent action branch was adopted from its own clean worktree with the same trunk.
Existing local registration does not need repetition on every run.

For a parent correction, commit on its owning branch. Propagate normal additive parent merges only where required.
Validate changed descendants and use ordinary `git push` for each authorized branch.
Run `gh stack view --json`, then reconcile native membership with the PR-number `link` command.
The tool's saved base can be historical. Compare actual ancestry; do not infer that a rebase is required from age alone.

For new dependent work, create a dedicated clean worktree from the required parent's head.
Use `gh stack add <existing-new-branch>` from the current stack's top owner to adopt that dependent branch.
Inspect `add --help` and both worktrees first. Do not steal a checked-out branch or stage unrelated files.
Keep independent work in a separate stack with the actual prerequisite as trunk.
Push with normal Git. Create the scoped PR with `gh pr create`, preserving its immediate prerequisite base.
Then append its PR number with `gh stack link --remote origin <stack-number> <new-pr-number>`.
Use explicit PR URLs if a numeric identifier could be ambiguous.
Re-read remote membership and verify original PR numbers, bases, heads, ready status, and terminal checks.

If adoption requires a history rewrite, forced update, replacement PR, or destructive metadata operation, stop that operation.
Report the exact blocker and required additional authorization. Continue unaffected work.
No stack command grants merge, deployment, billing, credential, or security-setting authority.
