# Repeatable implementation sessions

Use Beads for scope, dependencies, claims, and completion evidence. Pull requests contain the code review record.

## Start a session

1. Read `AGENTS.md`, `CONTRIBUTING.md`, and relevant domain documents.
2. Inspect `git status --short`, `git diff --stat`, and `git worktree list`.
3. Preserve existing edits. Do not reset, stash, clean, or rebase another session's work.
4. Start the session command under the shared lock:

   ```sh
   go build -o dist/work-session ./scripts/work-session && ./dist/work-session sh
   ```

   Run all remaining session commands inside this shell. Exit the shell when the session ends.
   An automation can replace `sh` with its implementation command and arguments.
   Invoke the compiled executable directly. `go run` changes nonzero command statuses to status 1.
   Do not lock only the test command. Hold the lock through issue updates, commits, pushes, and pull request creation.
5. Verify read access with `gh repo view` and `gh pr list --state open`.
6. Run `bd prime`, `bd ready --json`, and `bd list --status in_progress --json`.
7. Read the selected issue with `bd show ID`. Check its dependencies and existing pull requests.
8. Claim an unassigned issue with `bd update ID --claim`. Do not take an active claim from another worker.
9. Fetch the default branch. Create a dedicated branch and a clean worktree for the selected issue.

The lock uses the Git common directory. All worktrees of one clone share the lock.
A competing session exits with status 75 before it runs its command.
A command keeps its exit status. The operating system releases the lock when the command exits.
The command must wait for its child workers before it exits. Do not detach implementation workers.
The lock requires macOS or Linux and a filesystem that supports advisory file locks.
Separate clones and other machines need a shared coordination service. This lock does not coordinate those clones.
Beads claims remain necessary. The lock does not stop a person or tool that bypasses the wrapper.

Keep the lock file in place. Do not delete the file to clear a lock.
A persistent file does not mean that a session still holds the lock.
If status 75 persists, inspect the active process before you stop that process.

## Select and implement work

Choose one bounded issue with testable acceptance criteria. Prefer work that unblocks an epic.
Check the committed source against pending local changes before you select a migration baseline.
If an issue requires an unpublished prerequisite, report that dependency. Do not copy unrelated edits into the branch.

Add a regression test for the changed behavior. Use synthetic fixtures and local provider servers for integration tests.
Preserve public interfaces, queue transactions, delivery identity, transcript erasure, and evidence privacy during migration.
Keep product policy changes separate from package moves.

## Validate

Fixture Git commands must use the shared test environment builder.
Remove inherited repository-location and local configuration overrides before fixture commands.
Apply the same environment to fixture session commands and command builds.
Preserve the explicit synthetic identity and isolated Git configuration.
The production session command retains caller-supplied repository selection.
Never test contaminated fixture commands against the shared checkout.
The regression uses a disposable outer repository and verifies that its HEAD and index remain unchanged.

Run the focused tests first. Then run:

```sh
task --exit-code validate-offline
```

This command runs uncached Go tests, uncached race tests, vet, Task checks, offline evaluation, and a temporary binary build.
It disables inherited live-test opt-ins, provider tokens, calibration approval, private corpus selection, and external report destinations.
It prints one result for each gate and returns failure if any gate fails. Later gates still run after a failure.
The validation command sets `INDEX01_EVAL_REPORT_DIR` to `dist/evaluation` for all gates. Cancellation stops later gates.
The script returns status 129, 130, or 143 for HUP, INT, or TERM. Use Task with `--exit-code` to preserve that status.
It does not isolate network access at the operating system level. Synthetic integration tests can use local HTTP servers.
The command uses the installed Go toolchain and dependencies. Normal Go module downloads can occur if dependencies are missing.

Run these additional checks when the required tools are available:

```sh
git diff --check
shellcheck scripts/*.sh
actionlint .github/workflows/*.yml
task test-compose-runtime
```

Container runtime tests require a Docker daemon and the pinned builder image.
Manifest validation requires kubeconform and its schemas. Security checks require the versions listed in `CONTRIBUTING.md`.
Record missing tools, downloads, failed checks, skipped tests, and baseline failures separately.
Do not run live provider tests, deployments, releases, credential changes, or merges without separate authorization.

## Native stack registration

Use the official `gh stack` extension for dependency registration and future dependent layers.
Follow [the native stack workflow](gh-stack-workflow.md) for the registered graph, isolated worktrees, and history-preserving commands.
Version0.2.1 stack push uses force-with-lease. Use normal Git pushes and PR-number-only stack links under the current authorization.

## Continue before review or merge

Review and merge timing do not block implementation. Keep advancing the approved epic scope while pull requests remain open.
Use an unmerged prerequisite branch as the base when the next slice needs that code.
Keep independent work on separate branches based on the default branch.
Do not create artificial dependencies because pull requests change the same file.
Inspect changed lines, module constraints, public contracts, and tests before assigning a dependency.

Test the incremental child change and the cumulative stack. Record both results in the child pull request.
A known baseline failure blocks the affected acceptance gate. It does not block unrelated implementation or independently testable descendants.
Do not carry an unexplained failure forward. Establish its baseline and record its effect on each descendant.
Do not enable deployment, live providers, verification authority, or credential changes to bypass a blocked gate.

Keep the issue open while required acceptance evidence remains unverified.
Distinguish implementation evidence, review state, merge readiness, and epic completion in the handoff.
A draft can contain tested work that future slices use. An open pull request does not prove acceptance.
See [the pull request dependency map](pr-dependency-map.md) for the current branch order and overlap decisions.

## Record review separately

Record the reviewed base and head, reviewer source, scope, findings, and unresolved limits.
Check whether an automated reviewer completed a review or only reported a skip.
Do not treat passing tests, builds, CodeQL, or a successful skip status as completed source review.
Record regression tests separately from the source review that found and checked a defect.
After a correction, record the correction revision and the follow-up review scope.
A child correction does not change the original parent head. State which complete stack contains the fix.
Keep draft status while required acceptance or security gates remain unresolved.
See [the first-slice review evidence](first-slice-review.md) for the initial review and findings.

## Publish and resume

1. Inspect the final diff and untracked files. Exclude private payloads, credentials, and unrelated edits.
2. Record the issue ID, source revision, test commands, results, prerequisites, and remaining dependencies in Beads.
3. Commit the focused change and push its dedicated branch.
4. Create a pull request. Include the issue ID, acceptance evidence, and known failures.
5. Read the pull request checks. State pending checks as pending.
6. Close only the issue whose acceptance criteria are complete. Keep parent epics open until their criteria pass.
7. Record the branch, worktree, commit, pull request, base, and next action in Beads before the session ends.

On resumption, acquire the lock and read this record before editing.
Match the worktree branch and commit to the record. Inspect all local differences.
Reuse a clean matching worktree. If another session changed that worktree, preserve the changes and use a new worktree.
Check remote pull requests before creating another branch or pull request for the same issue.

Use the default branch as the pull request base when the change is independent.
If a required change exists only in an open pull request, branch from that pull request's head.
Set the dependent pull request base to the prerequisite branch.
Document the prerequisite link and review order in both pull request descriptions.
Keep each child diff limited to its own issue. Run the full tests on the combined stack.
If the base changes, inspect the new diff and repeat affected validation.
Preserve published history when a prerequisite changes. Do not force-push shared branches.
If a reviewable child needs a new head, create a replacement child from the required base.
Link the old and replacement pull requests in both descriptions. Preserve the original branch and discussion.
Close a superseded pull request only after its replacement preserves its complete scope and validation evidence.
Record whether human reviews exist before replacing a pull request.
If preserving both history and a reviewable diff requires an unauthorized action, request approval for that specific action.
Do not merge pull requests as part of stack maintenance.

## Initial implementation evidence

The first slice adds `scripts/work-session` and `scripts/validate-offline.sh`.
It starts from commit `85f2a46` and does not include pending application fixes.
The lock tests use a disposable Git repository and linked worktree.
They cover contention, command failure, normal exit, termination, invalid commands, and non-repository paths.
The validation tests inject synthetic live settings and a failed gate.
They verify isolation, failure reporting, and execution of every remaining gate.
`task test` includes the validation command regression test.

## Epic scope and stopping conditions

This is a scope snapshot from 2026-10-16. Beads remains the source of truth.
Read `bd show ID` for current acceptance criteria and dependency edges before each slice.
The IDs below refer to local Beads records. They are not GitHub issue numbers.
Do not create substitute GitHub issues or treat this table as a second tracker.

| Epic | Baseline state | Completion evidence |
| --- | --- | --- |
| `index-01-hook-quf` — Durable delivery | Open. Synthetic delivery implementation exists. Final acceptance remains open. | Every child completes. Tests, race tests, vet, and clean build pass. Restart and partial batches preserve completed siblings. Intake persists before HTTP 202. Replay returns HTTP 200 without duplicate delivery. Operations and retention are documented. |
| `index-01-hook-677` — TypeSafe recalibration | Open. Verification stays disabled pending calibration. | Labeled positive and negative corpus; field-wise and Choice comparison; false accepts, false rejects, review volume, latency, and token use. Thresholds use held-out labels. Record an enable, defer, or reject decision. |
| `index-01-hook-21n` — Calibration corpus | Open. `index-01-hook-c3g` is the canonical corpus execution issue. | Corpus reaches contract targets without relaxed gates. Complete the approved review and field-wise calibration. Report the one-sided 95 percent false-accept bound and the private real-item review-volume comparison. |
| `index-01-hook-b7y` — Architecture migration | Open. Package extraction has not started on `main` at `85f2a46`. | Importable application use cases own narrow ports. Production, tests, and replay share policy. Preserve deduplication, atomic freeze, leases, reconciliation, erasure, evidence integrity, and privacy. Preserve all interfaces and builds. Enforce dependency rules in CI. Document the final package map. |
| `index-01-hook-q1k` — Dashboard tooling | Open. Child evaluations remain open. | Each templ, sqlc, and goose evaluation records evidence and an adopt, defer, or reject recommendation. Summarize a safe adoption order. |

Architecture migration has these sub-epics:

| Sub-epic | Completion evidence |
| --- | --- |
| `index-01-hook-b7y.25` — Contracts and baselines | Identify pending baseline differences. Assign contract and invariant owners. Record reproducible behavior validation. |
| `index-01-hook-b7y.26` — Workflow and providers | Run workflow through application-owned ports. Keep transitions atomic. Preserve reconciliation and retry behavior with scripted adapters. |
| `index-01-hook-b7y.27` — Assessments and evidence | Use interchangeable typed assessment adapters. Keep shadow evidence separate from delivery authority. Preserve provenance and expiry. |
| `index-01-hook-b7y.28` — Interfaces and operations | Call application services from inbound interfaces. Keep dashboard reads read-only. Test maintenance safety and response contracts. |
| `index-01-hook-b7y.29` — Runtime and compatibility | Select production and offline adapters explicitly. Close storage last. Preserve assets, commands, metadata, and release builds. |
| `index-01-hook-b7y.30` — Enforcement and completion | Reject boundary violations in CI. Pass required regression and runtime checks. Match final documentation to source. |

Use this dependency order as implementation guidance. Individual Beads blocking edges take precedence.

1. Reconcile pending source differences under `index-01-hook-b7y.2.1` before selecting the migration baseline.
2. Complete boundary inventory and contracts under `.1.1` and `.1.2`. Record compatibility checks under `.2.2` after its prerequisite.
3. Establish application types and stable identity. Then adapt providers, atomic queue operations, and deterministic workflow execution.
4. Extract assessments, evidence, observation, and replay services after their required contracts exist.
5. Extract inbound interfaces and operations after their application services exist.
6. Compose runtimes and preserve builds. Enforce boundaries and audit the completed migration.
7. Evaluate dashboard tooling independently. Do not combine tool adoption with behavior-preserving extraction.

The architecture documentation baseline exists in local commit `74f79ad` on `codex/hexagonal-architecture-plan`.
It was not present on remote `main` during this review.
The main checkout also contains pending health-expiry and moved-item reconciliation fixes, tests, and documentation changes.
Those changes remain outside this slice. Do not duplicate or transplant them.
The existing publication worktree remains outside this slice.

Corpus authoring requires human-written prose under `docs/typesafe-corpus-authoring.md`.
An agent must not write case text or relax corpus gates.
Some corpus issues reference a builder absent from `main`; verify the source revision before attempting those steps.
Paid calibration calls, private comparisons, production verification, release publication, and live acceptance require separate authorization.
When a required gate exceeds the authorized scope, record the blocker and stop that path.
Do not mark an epic complete because the available offline checks passed.
When all listed epic criteria are satisfied, stop implementation. Do not expand the recurring scope automatically.

## Initial validation baseline

At `85f2a46` plus this tooling slice, these checks passed on macOS with Go 1.26.6:

- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`
- `task test`
- `task eval`
- `go build -o TEMPORARY_PATH .`
- `go test -count=1 -timeout 45s ./scripts/work-session`
- `./scripts/validate-offline_test.sh`
- `shellcheck scripts/validate-offline*.sh`

Live model checks remained skipped. Offline evaluation does not establish model accuracy or calibration success.
Existing synthetic tests cover replay, zero-item extraction, mixed items, completed siblings, expired leases, and transcript erasure.
The pending moved-item and health-expiry regressions require their selected source baseline.
Full container, infrastructure, and security results are separate from these offline results.

The remote had six open dependency pull requests during this review:
[#26](https://github.com/klauern/index-01-hook/pull/26),
[#27](https://github.com/klauern/index-01-hook/pull/27),
[#29](https://github.com/klauern/index-01-hook/pull/29),
[#30](https://github.com/klauern/index-01-hook/pull/30),
[#31](https://github.com/klauern/index-01-hook/pull/31), and
[#32](https://github.com/klauern/index-01-hook/pull/32).
All used `main` as their base. GitHub Issues contained no open records.
PR #29 had successful test, container, manifest, and security checks.
PR #30 had successful test, container, and manifest checks, with failed security checks.
PRs #26, #27, #31, and #32 had failed test checks, with successful container and manifest checks.
PRs #31 and #32 also had failed security checks. Read current checks before relying on this snapshot.
The [PR #32 CI run](https://github.com/klauern/index-01-hook/actions/runs/38648638258) reports Go standard-library vulnerabilities.
That log identifies Go 1.26.9 as the fix for findings in Go 1.26.6.
This slice does not change the toolchain or security configuration.

Additional checks passed for this slice:

- `shellcheck scripts/*.sh`
- `actionlint .github/workflows/*.yml`
- `./scripts/ci-manifests.sh`: both manifest sets passed; each had 10 valid resources and no errors or skips.
- A uniquely tagged Docker build and `INDEX01_TEST_IMAGE=UNIQUE_TAG ./scripts/compose-volume_test.sh`.
- `task test-release-artifacts`
- `./scripts/publish-public-history_test.sh`
- `git diff --check`

`./scripts/clean-public-source_test.sh` failed at its exported Go suite.
`TestJoinAndPrivatePath` in `scripts/ticktick-pull` expects repository metadata in a source export that omits `.git`.
The same focused test failed in an unchanged `git archive` of `85f2a46`.
The pending checkout already changes this test. Preserve that work under the baseline reconciliation issue.
The initial export attempt and the attempt after staging both failed. Staging did not resolve the baseline failure.
Container-based public end-to-end deployment and disposable-cluster infrastructure tests were not run.
Local govulncheck and complete-history secret scans were not run during this slice. Check remote CI for their results.

## October 18 source status update

The former primary-checkout edits are now independent local commits on `cleanup/worktree-review` at `77b7594`.
The checkout is clean. The original contract slice excluded those commits. The readiness repair now carries the scoped export-fixture patch through #40.
Beads `index-01-hook-b7y.2.1` records health `3411659`, reconciliation `d0ec0f8`, export fixture `a0c99d1`, and related documentation.
Preserve these contributions and review their publication and baseline selection separately. Do not copy them into an unrelated contract child.
Earlier pending-source descriptions above are historical snapshots. They do not describe current uncommitted edits.

## Green and ready gate

The [pull request readiness policy](pr-readiness.md) applies to every current and future pull request.
This current policy supersedes earlier allowances for inherited baseline failures in this document and descendant snapshots.
Do not finish at submission or accept known inherited security failures as the final result.
Assess all open PRs, including independent changes and every stacked descendant.
Require source assessment, terminal green final-head checks, and ready status before the final handoff.
A draft is temporary. Mark eligible PRs ready so draft-gated checks and reviews can run, then verify their results.
A successful skipped-review notice does not establish completed source review.
Record exact heads, bases, dependency edges, merge order, incremental and cumulative test results, and unresolved reviews.
Fix legitimate findings. Do not disable checks, lower thresholds, add blanket exclusions, or change protection settings to obtain green.
Pending, canceled, stale, and failure-induced skipped checks do not pass this gate.
Report external or manual blockers precisely. Continue unaffected repair work without inventing a pass.
Keep new feature slices paused while the all-PR readiness pass has actionable failures.
Review readiness does not establish integration or epic completion on `main`.

The three PR #33 validation defects originally corrected in PR #36 are now corrected additively at this parent.
The source patch comes from `276496bc1526d2ce266d86fef968781ed7db0a29`.
Regression tests use the compiled session binary, isolate report paths, and verify cancellation stops subsequent gates.
Preserve the original PR #36 commit and review history while propagating this parent correction through the stack.

For included CodeRabbit review requests, use [free OSS review sessions](free-oss-reviews.md).
Keep current queue state in Beads. Do not create duplicate schedules.


## Continue after parent merges

Inspect the actual merge commits and remote branch heads before changing descendants.
A squash or rebase can remove commit ancestry while preserving source content.
Use isolated integration branches and additive merges when published history must remain intact.
Resolve each conflict from source intent. Preserve review evidence and all parent corrections.
Validate the integration candidates before a normal, non-forced push to the existing PR branches.
Retarget open roots to current `main`. Keep dependent PRs based on their immediate open parent.
Use the supported `gh stack` workflow in [merge recovery](stack-merge-recovery.md) to reconcile real remote and local metadata.
Keep merged native-stack entries as history. Do not attempt to remerge them.
Treat old-head review, byte-identical source, local conflict assessment, and new hosted checks as separate evidence.
Do not request unavailable bot-author reviews or repeat diagnostic requests after a merge-only update.
