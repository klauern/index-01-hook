# Selected source baseline before package extraction

Date: 2026-10-21. Beads `index-01-hook-b7y.2.1` owns this source disposition.

The integrated main baseline is `f5c99b6d105f583599eb915d141d5e43fd1d1595`.
All former PRs, including dependency recovery #45, are merged. The temporary bounded review queue remains closed.

This dedicated parent publishes two preserved local corrections before application package moves:

- `34116598db3badf3526a383784c7d079885d0448`: expire stale provider latency readiness signals after five minutes while retaining diagnostics.
- `d0ec0f8123aae7c99cebbf5d164b32d935c41f14`: reconcile moved remote items through bounded reads and prevent another create after an unconfirmed ambiguous write.

The original `cleanup/worktree-review` checkout and its commits remain intact.
Cherry-pick provenance records the original revisions. No published branch or source history is rewritten.
The export-fixture correction from `a0c99d1` already reached main through the earlier readiness repair.
Independent agent guidance and architecture-map commits remain preserved on the original branch.
They are excluded from this implementation; the reviewed architecture inventory and ADRs remain the migration reference.

## Preserved behavior and corrections

Recent provider latency still degrades readiness. Old latency observations expire; their values and failure flags remain visible.
Reconciliation scans at most 100 listed projects plus the task inbox within the existing 30-second request budget.
Moved items and removed or remapped aliases can still resolve the original remote identity.
Edited content, duplicate markers, malformed project identities, or incomplete visibility require review.
A missing marker does not prove that an earlier create failed. The worker records needs_review and never issues another create from that uncertainty.
Authentication, safe transient retries, extraction-attempt fencing, completed siblings, and evidence retention remain tested.
No schema, credential, provider permission, routing, active-verification, or deployment setting changes.

## Validation and next boundary

Run focused health, moved-item, ambiguous-write, deadline, restart, and fencing regressions before full offline validation.
Compare the new regression expectations with unchanged main in a disposable source export.
Use synthetic HTTP fixtures and real temporary SQLite databases. Live providers and private calibration remain unrun.
Record exact final heads and hosted checks in Beads before marking the PR ready.

Application value extraction depends on this selected source baseline. Later slices still own date policy, typed failures, use cases, provider ports, and runtime composition.
This baseline does not complete the architecture or delivery epics.
