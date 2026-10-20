# Repair assertions after Dependabot recreation

Dependabot can recreate an action upgrade after its base changes.
The recreated branch can preserve the workflow upgrade while losing an earlier test correction.
Read the actual remote head before changing or pushing a branch.
Create an isolated repair branch from that head. Preserve unrelated checkouts and historical branches.

Keep the exact action-pin assertions synchronized with the intended upgrade.
Update only the changed action assertion in each independent PR.
Keep the full commit hash, immutable builder, release gates, artifact checks, and forbidden-event assertions.
Do not replace the exact hash assertion with a version tag or remove the assertion.

The October 20 updates use these upstream release commits:

| PR | Action release | Commit |
|---|---|---|
| #27 | docker/setup-buildx-action v4.4.1 | f87e5991a6d7451dcb8d9637bfbc97413f497069 |
| #31 | actions/upload-artifact v7.0.2 | cf430e030ddbb5b0abf93d22962f4752f3646cd9 |

Verify each hash against the upstream release before publication.
Reproduce `TestReleaseWorkflowIsGatedAndImmutable` before the correction.
Run the corrected test, full offline validation, lint, and security checks.
Use isolated source exports to confirm that old pins and mutable tags still fail.
Push normally only when the fetched remote head remains an ancestor of the correction.
Inspect the actual final-head CI jobs and readiness state before reporting success.
Track exact heads, logs, and outcomes in Beads. Preserve existing review entitlement holds.
