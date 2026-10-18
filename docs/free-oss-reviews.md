# Free OSS review sessions

Use Beads task `index-01-hook-b7y.25.6` for current requests, results, findings, and retry times.
This document defines the workflow. Beads remains the queue state authority.
Do not create a second scheduler. The parent task manages scheduling.

## Included capacity

The repository is public and had zero stars on October 18, 2026.
CodeRabbit notices explicitly identify the free OSS allowance.
The configuration label `Advanced` does not establish paid capacity or a paid subscription.

[Official plans](https://docs.coderabbit.ai/management/plans) distinguish OSS access from the summary-only Free plan.
OSS reviews have an allowance of 1–10 per developer per hour, additionally scoped per repository.
Public repositories below ten stars require manual review requests.
The observed review on PR #33 reported one included review per hour.
Do not assume each PR has separate capacity or that the maximum advertised rate applies here.

[Official rate limits](https://docs.coderabbit.ai/management/rate-limits) use rolling windows.
Capacity follows the PR author identity. A new PR does not reset that author's allowance.
Use `@coderabbitai rate limit` to query availability without starting a review.
Do not change authors, accounts, repository identity, or settings to bypass a limit.
A rate-limited check can succeed when no review ran.

## Author entitlement

Available rate capacity does not establish review entitlement for the PR author.
On October 18, the manual #29 request was skipped despite confirmed available capacity.
The [bot notice](https://github.com/klauern/index-01-hook/pull/29#issuecomment-6048264884) requires an assigned review seat for `app/dependabot`.
The same author owns #26, #27, #31, and #32. Their shared-author impact is inferred; they were not retried.
Keep all five original PRs in the queue as unreviewed while this blocker applies.

An hourly wait or a full-review command does not establish the missing author entitlement.
Under the free-only scope, do not assign seats, change billing, replace PRs, or change author identities.
[Seat management](https://docs.coderabbit.ai/management/seat-assignment) can have billing effects.
Beads `index-01-hook-b7y.25.6.2` records the required entitlement and cost verification before a retry.

For this review queue, defer #34 until its parent #29 can receive a review.
Continue eligible independent original PRs, such as the human-authored #35 after verified parent #33.
This does not count the blocked parent as reviewed or transfer its review through another author.

## Serialized requests

1. Hold the common-directory session lock. Read repository instructions and current Beads state.
2. Fetch current PR heads, bases, review submissions, inline comments, and bot notices.
3. Key each queue entry by repository, PR number, current head, and requested review scope.
4. If a matching request is pending, monitor that request. Do not submit a duplicate.
5. Select a prerequisite before its descendants. Address valid findings before requesting descendant reviews.
6. Query the relevant author's included capacity. Record the bot reply URL and UTC timestamp.
7. If capacity is unavailable, record the advertised retry time. Request nothing before that time.
8. When capacity is available, submit one review request. Wait for its outcome before submitting another review.
9. Use the [documented commands](https://docs.coderabbit.ai/reference/review-commands).
   Use `@coderabbitai review` for new changes. Use `@coderabbitai full review` only when a complete repeat is necessary.
10. Keep the reviewed head unchanged while the review runs. Batch fixes locally before one push.
11. Verify each finding against source. Add regression coverage for a valid behavior defect.
12. Fix a shared defect at its owning parent. Propagate additive parent merges only where required.
13. Reply with the correction commit and validation evidence. Resolve only findings whose disposition is verified.
14. Recheck the exact reviewed commit. A summary, skip, or successful check alone is not a completed review.
15. Run relevant tests and full cumulative validation after code changes. Verify terminal green checks and ready status.
16. Reserve the next included slot for verification when a correction needs another review.
17. Record current head, reviewed head, request URL, outcome, pending findings, and next eligible time in Beads.

Never enable paid usage, on-demand continuation, upgrades, security scans, or generated fixes that can create charges.
A feature excluded from the plan remains blocked after an hourly wait. Report that blocker instead of retrying indefinitely.
A displayed retry interval is earliest eligibility, not guaranteed capacity. Re-query capacity when the scheduled run starts.
No scheduler or protection setting changed during this review pass.

## Initial queue snapshot

This snapshot does not replace current Beads state. Refresh heads before each request.
Order: #40, #33, #29, #34, #26, #27, #31, #32, #35, #36, #37, #38, #39.
Parent prerequisites take precedence over this order when fixes change the dependency graph.
The graph is #40 before every root; #29 before #34; #33 → #35 → #36 → #37 → #38 → #39.

| PR | Author | Snapshot head | Initial review state |
| --- | --- | --- | --- |
| [#40](https://github.com/klauern/index-01-hook/pull/40) | `klauern` | `7a3e9c91ba6a64a77381b85ff2c0b246a4bd3d70` | Review requested after included-capacity confirmation. |
| [#33](https://github.com/klauern/index-01-hook/pull/33) | `klauern` | `12b29bff4a398949c4226b4d838c2bab34293eca` | Old head cc0dda3 reviewed; three findings fixed at 12b29bf. Current head awaits verification. |
| [#29](https://github.com/klauern/index-01-hook/pull/29) | `app/dependabot` | `79ce48abffcf17cba736d71ca6d6d18fc0c58e1f` | No submitted review. Manual request required. |
| [#34](https://github.com/klauern/index-01-hook/pull/34) | `klauern` | `171101a2c2a6bf9550f8af87110678618f8cf650` | No submitted review. Manual request required. |
| [#26](https://github.com/klauern/index-01-hook/pull/26) | `app/dependabot` | `9905976539481786c0fa45a06d1bd55c260c51ef` | No submitted review. Manual request required. |
| [#27](https://github.com/klauern/index-01-hook/pull/27) | `app/dependabot` | `b35135c6daa376bd5745499297b4ff1a347c1557` | No submitted review. Manual request required. |
| [#31](https://github.com/klauern/index-01-hook/pull/31) | `app/dependabot` | `f49c0c9e367810731d23f4f0b7492270324b2adf` | No submitted review. Manual request required. |
| [#32](https://github.com/klauern/index-01-hook/pull/32) | `app/dependabot` | `73354309ef82cbbcd12db82e75aaae5ffd15ea14` | No submitted review. Manual request required. |
| [#35](https://github.com/klauern/index-01-hook/pull/35) | `klauern` | `ebb23035ca9ff138f02f4b04d5fb0c35a34d4902` | No submitted review. Manual request required. |
| [#36](https://github.com/klauern/index-01-hook/pull/36) | `klauern` | `584f028bd18718b7d1aef094fcc01847baa7be51` | No submitted review. Manual request required. |
| [#37](https://github.com/klauern/index-01-hook/pull/37) | `klauern` | `9fc07e6af45e5ef053f191fd51e8778b56f81e8f` | No submitted review. Manual request required. |
| [#38](https://github.com/klauern/index-01-hook/pull/38) | `klauern` | `6d68bebc42f5085e823d7b04ecb9ebcca208bff0` | No submitted review. Manual request required. |
| [#39](https://github.com/klauern/index-01-hook/pull/39) | `klauern` | `6f9ba3d4ba8b9ebdb98b0a4b82cbb8f8895d5b1b` | No submitted review. Manual request required. |

The #40 [limit notice](https://github.com/klauern/index-01-hook/pull/40#issuecomment-6252285277) reported 46 minutes at 16:31:59 UTC.
Its earliest advertised retry was approximately 17:17:59 UTC on October 18.
A [capacity reply](https://github.com/klauern/index-01-hook/pull/40#issuecomment-6252866022) confirmed availability at 17:23:42 UTC.
The [single review request](https://github.com/klauern/index-01-hook/pull/40#issuecomment-6252871392) followed that confirmation.
Check Beads for the outcome and the next verified retry window.

The previous #33 review covered `cc0dda3752269c94b01056a69296434a3a080fb1`.
Its three original findings have fixes and resolved threads. Its docstring-coverage warning requires separate disposition.
Do not describe that old review as verification of `12b29bf` or later heads.

A [capacity query on #26](https://github.com/klauern/index-01-hook/pull/26#issuecomment-6252887753) reported 58 minutes at 17:25:39 UTC.
Its earliest advertised retry was approximately 18:23:39 UTC on October 18.
This reply does not prove an independent bot-author pool. Keep one serialized project queue.

## Completed pass on the original prerequisite head

CodeRabbit finished the #40 pass at 17:29:20 UTC on October 18.
The [review-info comment](https://github.com/klauern/index-01-hook/pull/40#issuecomment-6252285277) reports no actionable comments.
Its coverage marker identifies source and covered commit `7a3e9c91ba6a64a77381b85ff2c0b246a4bd3d70` as reviewed.
The GitHub submitted-reviews endpoint has no entry. Record this as a completed bot pass published in a comment.
Do not call it human approval or a submitted GitHub review.
The same comment reports a warning for documentation coverage on three touched tests.
Document those tests without lowering the threshold. Verify the updated head in the next included slot.
The comment confirms one included review per hour and zero remaining reviews after this pass.

CodeRabbit acknowledged the original #33 fixes in all three review threads after receiving correction evidence.
Those acknowledgments do not replace an updated-head review or remeasure documentation coverage.
