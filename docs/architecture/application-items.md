# Application item policy

Beads `index-01-hook-b7y.3.5` owns this extraction. The prerequisite is PR #47.
The date stack and identity adapter change are independent. Their heads remain unchanged.

The application package owns item count, title, content, alias, tag and priority policy.
Limits count UTF-8 bytes. Title and alias checks use trimmed values.
Content has a 32 KiB limit before provider marker encoding.
Provider response sizes and encoded payload limits remain adapter policy.

Configured aliases use lowercase, trimmed space, duplicate removal and sorted output.
Configured aliases reject empty values and embedded carriage returns, newlines or NUL bytes.
Frozen aliases retain the previous length-only check. This extraction does not rewrite stored aliases.
Tags preserve case and input order. Validation precedes duplicate removal.
Notes reject all task fields. Legacy tasks retain their existing validation errors.

## Validation and continuation

Run focused policy and root compatibility tests before adapter adoption.
The root compatibility test compares application results with the existing production boundaries.
Run `GOTOOLCHAIN=go1.26.10 task --exit-code validate-offline` on each published layer.
Run lint, security, manifest and container gates. Verify exact final-head hosted checks after readiness.
Record hosted CodeQL separately from manual analysis and source review.
Live providers and private corpus validation remain outside this offline slice.

Register the local stack with `gh stack init --base codex/application-values-20261021 codex/item-policy-20261021`.
After the adapter layer exists, use supported `gh stack add` and PR-number-only `gh stack link`.
Use normal Git pushes. Preserve native stacks49 and52 and historical stacks41 and42.
Merge the prerequisite first. Then reconcile the actual native root and GitHub base to main.
Do not merge a descendant into an obsolete parent branch.
