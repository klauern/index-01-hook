# Pull request readiness

This policy applies to every current and future pull request.
It separates review readiness from integration on `main`. It does not authorize a merge or deployment.

## Required result

Every pull request must have source assessment, current validation evidence, ready status, and terminal green checks.
Do not accept inherited security failures as an adequate final result.
Do not disable checks, lower thresholds, suppress legitimate findings, or change protection settings to obtain green status.
A draft is a temporary work state. Before handoff, mark an eligible pull request ready and verify checks that start after that change.
A successful skipped-review notice is not completed source review.

Read the repository rules before each pass. The inspected main ruleset requires `test`, `container`, `manifests`, and `security`.
Inspect non-required checks and review threads as well. Report an external or manual blocker precisely.
Pending, canceled, stale-head, and failure-induced skipped checks cannot establish a fully green result.
If a check is intentionally inapplicable, record its exact state and reason. Do not describe it as a successful test.

## Repeatable gate

1. Hold the shared clone lock for the complete session. Inspect all local edits and preserve unrelated contributions.
2. Read authoritative Beads, repository instructions, current pull requests, exact heads and bases, reviews, and rulesets.
3. Record every open pull request, including independent changes and all stacked descendants.
4. Diagnose shared failures. Repair a shared prerequisite once and merge it additively into dependent branches.
5. Preserve published commits and review context. Do not force-push shared branches or silently replace other contributions.
6. Run focused incremental tests and full cumulative tests on the actual changed stack.
7. Run race tests, vet, lint, build, offline evaluation, synthetic integration checks, and the applicable security scans.
8. Assess source independently of test results. Address valid review findings and record reasons for rejected findings.
9. Push the dedicated changes. Record the exact base, head, dependency graph, merge order, commands, results, and prerequisites.
10. Mark eligible pull requests ready. Wait for final-head CI and any newly triggered checks or reviews.
11. Fix introduced or inherited failures. Revalidate each changed descendant after propagation.
12. Report each final ready/draft state, exact head, terminal check results, unresolved reviews, and external/manual blockers.

A blocked external service does not permit a fabricated pass. Record the blocker and continue unaffected repair work.
Do not resume new feature slices while the authorized all-PR readiness pass still has actionable failures.
Keep Beads work open when acceptance evidence remains unresolved.
Report overall epic completion only after the reviewed scope is merged and verified on `main`.

## Security toolchain repair

The October 18 audit found 11 called standard-library vulnerabilities with Go 1.26.6.
The [Go release history](https://go.dev/doc/devel/release#go1.26.9) records the fixes in Go 1.26.9.
The October 21 scan found GO-2026-6809 in Go 1.26.9.
Go 1.26.10 fixes this standard-library certificate validation vulnerability.
The current repair pins the compiler and all active builder references to Go 1.26.10.
The official multi-platform builder is:

```text
docker.io/library/golang:1.26.10@sha256:3745f1aa11822297b79113f5e84ce2bffeb039734571c39855809cfe9e5d7be6
```

The digest was resolved from the official Docker image before the change.
Local checks must select the patched compiler, for example with `GOTOOLCHAIN=go1.26.10`.
CI selects the version in `go.mod`. Keep contributor checks, release builders, and fixtures aligned.
This toolchain repair does not enable releases or change credentials, approval gates, or protection rules.
