# First-slice review evidence

This record separates source review from test execution and continuous integration (CI).
A successful check can report a skipped review. Read the review body before recording review completion.

## Skipped external reviews

CodeRabbit skipped draft review for these heads:

| Pull request | Reviewed input revision | Skip notice |
| --- | --- | --- |
| #33 | `cc0dda3752269c94b01056a69296434a3a080fb1` | [Draft review skipped](https://github.com/klauern/index-01-hook/pull/33#issuecomment-6206212907) |
| #34 | `883d3069bedfb89d51045046c80013fb791d198d` | [Draft review skipped](https://github.com/klauern/index-01-hook/pull/34#issuecomment-6206343750) |
| #35 | `1360f976279b8e971f7c369f1b538718b59be06d` | [Draft review skipped](https://github.com/klauern/index-01-hook/pull/35#issuecomment-6206356610) |

The successful CodeRabbit status on these heads is not completed review evidence.
No external reviewer was contacted. Review configuration and paid services were not changed.

## Independent local source review

Five fresh local reviewers inspected separate aspects of the first slice.
The scopes were lock lifecycle, offline validation, repository contracts, history and dependencies, and stack interactions.
A sixth local reviewer independently validated the findings and reproductions.
The implementation author then prepared the corrections. Local reviewers checked the resulting correction diff separately.

| Scope | Source review result |
| --- | --- |
| PR #33 against `85f2a46` | Three introduced correctness findings, listed below. |
| PR #34 against `077adf4` | No concrete finding. The child changes only the module requirement and two checksums. Cached module metadata and toolchain constraints were checked. |
| PR #35 against `cc0dda3` | No additional finding. It inherits the incorrect launcher example from #33. Dependency order matches local ancestry. |

This is a focused review. It does not establish full transitive dependency safety or replace the unresolved security gate.
It does not claim that CodeRabbit reviewed the code. Tests and builds are recorded separately below.

## Findings and corrections

1. **P2: The documented launcher loses command status.**
   `go run` returned 1 when the command returned 7 or lock contention returned 75.
   Invoke a compiled executable directly. The corrected workflow builds `dist/work-session` and runs that executable.
   The lock integration tests now build and invoke the actual command instead of invoking a test helper.
2. **P2: Validation preserves an inherited external report destination.**
   The first Go gate can overwrite `current-offline.json` outside the repository before Task resets the report directory.
   Set `INDEX01_EVAL_REPORT_DIR=dist/evaluation` before every gate.
   The regression test injects an external destination and checks the controlled destination in every command.
3. **P2: Cancellation cleanup can resume validation.**
   A TERM signal during the first gate allowed all six gates to run and return success.
   Keep cleanup on EXIT. Exit with status 129, 130, or 143 for HUP, INT, or TERM.
   The regression test sends each signal during the first gate and checks the status and command count.

The signal handler stops later gates. A noninteractive shell can defer the handler until its current foreground command finishes.
The command does not promise immediate termination of that foreground process.

The corrections are [PR #36](https://github.com/klauern/index-01-hook/pull/36), a child of #35.
The reviewed correction code is commit `276496bc1526d2ce266d86fef968781ed7db0a29`. The original reviewed heads still contain the findings.
Use the complete correction stack for acceptance. Do not describe an unchanged parent head as corrected.
No published branch needs a force-push or history rewrite to apply these corrections.

## Test evidence

The report-destination regression failed on the original validator before the correction.
The original cancellation failure and external report overwrite were reproduced independently with synthetic temporary data.
The direct compiled command returned 7 and 75 in independent reproductions; `go run` returned 1.

The correction's PR records exact test commands, results, source revision, and hosted CI results.
A passing test result does not close a source review finding without review of the correction.
A source review finding can be resolved while an unrelated security gate remains blocked.
Keep those states separate in Beads and pull request descriptions.
