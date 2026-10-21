# Application date policy

Beads `index-01-hook-b7y.3.2` owns the date-value and due-parser layer.
This independent date chain starts at application values PR47. Identity PR48 is not a prerequisite.
Use a separate native stack with PR47's branch as its base. Do not change stack49's existing membership or bases.

The application package owns DateParts and ParseDue. TypeSafeDateParts remains a root compatibility alias.
The production DeepSeek adapter consumes ParseDue and maps safe errors to its existing malformed category and operation.
Date-only values use an explicit location. Timed values retain their supplied offset and must match that location at the represented instant.
Missing dates, malformed values and invalid offsets retain their existing outcomes. Errors do not retain supplied date text.
No date parser reads a clock. Provider prompts, verification settings, schema and stored due dates remain unchanged.

Run application date tables, provider error compatibility and worker/SQLite retry-and-reopen regressions before full offline validation.
Use Go1.26.10, pinned builders, lint, manifests, vulnerability and secret scans. Verify all exact-head hosted checks after readiness.
Live providers and private calibration remain outside these synthetic checks.
Record exact heads, source coverage, review limits and final hosted checks in Beads.
The dependent layer owns semantic date resolution with explicit time and location.

## Explicit-clock resolution

Beads `index-01-hook-b7y.3.3` owns the dependent semantic resolution layer.
The application package resolves absolute, relative and weekday dates with caller-supplied time and location.
The root resolver delegates to this policy. Its compatibility name and result shape remain unchanged.
The former unused validator now also requires explicit time and location. No hidden wall-clock call remains in this date boundary.
The existing missing-confidence treatment, confidence threshold, year window and weekday rules remain unchanged.
The provider prompt and live verification policy remain unchanged. This extraction does not enable TypeSafe date authority.
Regression tests cover frozen clocks, midnight, spring/fall DST, impossible dates, year bounds and confidence precedence.
A disposable mutation that replaces supplied time with wall-clock time must fail the explicit-clock table.
Typed failure categories, remaining application validation, use cases and ports remain separate acceptance work.

## Registered graph and parent-merge gate

Native stack52 starts with PR50 then PR51. A confidence-validation child follows the semantic policy layer. Its base is PR47's application-values branch.
Native stack49 remains unchanged. PR48's identity policy is independent of the date implementation.
Merge PR46 and PR47 into main before merging the date chain.
After PR47 merges, inspect its actual merge commit and the current main tree.
Retarget native stack52 to main before merging PR50. Do not merge date work into an already merged parent branch.
Use additive integration when needed. Preserve all published commits and contributions.
Propagate a corrected PR50 into PR51, run incremental and cumulative checks, then push normally.
Use supported PR-number-only `gh stack link --base main <all current PR numbers in stack order>` to update the existing native stack.
Verify stack52's identity, its main base, PR50's main base, and PR51's immediate PR50 base.
Do not use force-push, destructive rebase or remote-unstack.
If PR48 also merged, preserve its identity inventory entry during any inventory conflict resolution.

The current nested bases are unprotected. No default-setup CodeQL run was scheduled on these date heads at publication.
[GitHub default-setup triggers](https://docs.github.com/en/code-security/concepts/code-scanning/setup-types) target the default branch or protected bases.
Record CodeQL as not run on this nested chain. Check scheduling again after readiness and after retargeting main.
Do not report inherited CodeQL runs from PR47 as scans of new date changes.
The local vulnerability, secret, lint and manifest scans and hosted security jobs remain required.
No security configuration was changed.

## Supplied-confidence validation

Beads `index-01-hook-b7y.3.4` rejects supplied non-finite probabilities and values outside zero through one.
Only confidence fields used by the selected semantic mode participate.
Invalid supplied confidence returns no candidate date, zero confidence and review required.
Valid low-confidence values retain their existing candidate and review behavior.
Missing confidence and unused fields retain their existing compatibility behavior.
The root resolver and explicit-clock validator consume the same guarded policy.
This change does not activate live verification or rewrite stored dates.
Failing-before and passing-after regressions cover every used field in absolute, relative and weekday modes.

An optional local CodeQL scan is separate from a hosted CodeQL check.
Use only public committed source, a verified official bundle and isolated outputs.
Record the exact source SHA, tool/query versions, commands, findings and exclusions.
Do not upload results or change scanning configuration to manufacture a hosted pass.
CodeQL not scheduled on a nested base remains a separate state from a successful local analysis.
