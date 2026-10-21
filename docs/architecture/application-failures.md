# Application failures and the consumed failure port

Beads `index-01-hook-b7y.3.6` records this part of the application migration.
The prerequisite is PR47. Item and date policy changes are independent.

The application package owns failure categories and metadata. The metadata contains a category, operation, numeric status and reason code.
Adapters must use static operation and reason codes. The contract retains no response body, transport cause or credentials.
The generic error string is fixed. Metadata lookup never formats the provider error.
Standard wrapped and joined errors use the first matching failure source.
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

Register a new local stack with explicit trunk `codex/application-values-20261021`.
Use supported gh-stack0.2.1 init/add/link commands, normal Git pushes and immediate-parent PR bases.
Preserve existing native stacks and published commits. After PR47 merges, reconcile the new native root to main before merging descendants.
If safe reconciliation needs destructive history changes, stop that action and report the required permission.
