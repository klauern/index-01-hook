# Application failures and the consumed failure port

Beads `index-01-hook-b7y.3.6` records this part of the application migration.
The prerequisite is PR47. Item and date policy changes are independent.

The application package owns failure categories and metadata. The metadata contains a category, operation, numeric status and reason code.
Adapters must use static operation and reason codes. The contract retains no response body, transport cause or credentials.
The generic error string is fixed. Metadata lookup never formats the provider error.
Wrapped and joined errors select ambiguous-write metadata before other failure sources, regardless of error order or nesting.
When no ambiguous write exists, lookup retains the first matching source, including custom `As` matches.
Ambiguous writes remain distinct from safe retries. A failed create must reconcile before another create when its outcome is uncertain.

`FailureSource` is a narrow error port. A provider keeps its root error shape and string while exposing application metadata.
The dependent adapter layer must connect production errors and worker decisions to this port.
This API layer alone does not establish production consumption.
Functional extraction, delivery and persistence ports remain separate migration work.
The extraction evidence envelope still has root dependencies. Do not claim that the complete extraction port is application-owned.

## Validation and continuation

Run `GOTOOLCHAIN=go1.26.10 task --exit-code validate-offline` on each layer.
Add scripted-provider regressions for authentication, malformed responses, review, safe retries and ambiguous-write reconciliation.
Compare public provider error shapes, safe diagnostics and reason codes before and after adoption.
The dependency guard must reject provider, HTTP and SQL imports from the application package.
Run lint, build, security, manifest and synthetic container gates. Verify final-head hosted checks after readiness.
Record manual CodeQL separately from hosted CodeQL and source review. Live/private validation is not enabled.

The original failure layers used native stack 59 with trunk `codex/application-values-20261021`.
That stack is closed after both layers integrated into PR47. Preserve its native history.
Use supported gh-stack0.2.1 commands, normal Git pushes and immediate-parent PR bases for surviving stacks.
After PR47 merges, reconcile actual surviving native roots to main before merging descendants.
If safe reconciliation needs destructive history changes, stop that action and report the required permission.

## Production consumption

DeepSeek and TickTick expose `FailureSource` without changing their public fields or error strings.
The worker now classifies wrapped failures through `FailureOf`. Failure decisions no longer inspect concrete provider error structs.
DeepSeek retains its reason-code mapping inside the adapter. The worker accepts only existing static reason codes for logs.
Unknown errors keep the malformed fallback. Unknown typed categories keep default state-machine outcomes and cannot become safe retries.
Scripted adapters can return application failures without constructing provider error classes.
The functional provider and persistence interfaces remain in the root. This slice does not complete Beads b7y.5.

## Joined failure recovery

PR57 and PR58 merged into PR47 on October 22. Their code is not yet on main.
Recovery tests reproduce a second create after a durable reopen when a retryable error conceals an ambiguous write.
The correction preserves ambiguous metadata across both join orders, nested joins and failure wrappers.
The durable regression requires one create, one reconciliation and a reconciled completion after an actual SQLite close and reopen.
This is synthetic regression evidence. It does not establish a production incident.
