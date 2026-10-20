# ADR-0002: Application contracts and migration invariants

Date: 2026-10-17

Status: Selected contract specification. Production extraction is not implemented.

Scope: `index-01-hook-b7y.1.2`, following the [boundary inventory](../architecture/boundary-inventory.md).
The source baseline is PR #37 at `72217cd`. Independent primary-checkout changes remain excluded.
The local architecture plan at `74f79ad` remains separate. This record does not publish or supersede that plan.

## Dependency decision

Put use cases and their consumed ports in `internal/application` during the later extraction tasks.
Keep neutral values with their application consumer. Do not introduce a separate domain framework.
Provider and persistence adapters import application contracts. Application contracts do not import adapters or root `main`.
Inbound adapters translate HTTP and command inputs. Root composition selects adapters and owns process lifecycle.
Replay invokes the same application policy with scripted ports. It does not copy production decision logic.
The root executable, public commands, response schemas, embedded assets, and storage schema remain compatible.
The package name below specifies the future boundary. The fenced source is type-checked, but is not linked into production.
Do not create unused duplicate production models before the corresponding extraction slice.

## Core signatures

The following source defines the core intake, extraction, assessment, delivery, and durable-transition contracts.
Only `context` and `time` are permitted imports in this specification.
Adapters convert legacy `QueuedTask` values to ordered items before crossing these ports.

```go
package application

import (
    "context"
    "time"
)

type RecordingID int64
type DeliveryID int64
type Fingerprint string
type LeaseOwner string
type ClaimAttempt int
type ExtractionFence struct { Owner LeaseOwner; Attempt ClaimAttempt }
type ItemKind string
const ( Task ItemKind = "task"; Note ItemKind = "note" )
type Item struct {
    Kind ItemKind
    Title, Content, ProjectAlias string
    Due *time.Time
    AllDay bool
    Priority int
    Tags []string
}
type ItemIdentity struct { Fingerprint Fingerprint; Index int }
type RemoteIdentity struct { ObjectID, ProjectID string }
type Intake struct {
    RecordedAtMillis int64
    Client, Trigger, Transcript, AudioFilename string
    AudioByteCount int64
    Fingerprint Fingerprint
}
type Receipt struct { ID RecordingID; Duplicate, Queued bool }
type Lease struct { Owner LeaseOwner; Duration time.Duration }
type Attempt struct { Total, Cycle, Reconciliations int; RecoveredLease bool }
type ExtractionClaim struct {
    ID RecordingID
    Fingerprint Fingerprint
    Transcript string
    Fence ExtractionFence
    Attempt Attempt
}
type DeliveryClaim struct {
    ID DeliveryID
    RecordingID RecordingID
    Identity ItemIdentity
    Item Item
    Marker string
    Attempt Attempt
    LastOutcome Outcome
}
type Stage string
const (
    Extraction Stage = "extraction"
    Assessment Stage = "assessment"
    Delivery Stage = "delivery"
    Reconciliation Stage = "reconciliation"
)
type Mode string
const ( Active Mode = "active"; Shadow Mode = "shadow" )
type StageMetadata struct {
    Stage Stage
    Mode Mode
    Provider, Model, PromptVersion, BuildCommit string
    StartedAt time.Time
    TimeZone string
}
type Outcome string
const (
    Success Outcome = "success"
    Retryable Outcome = "retryable"
    Authentication Outcome = "authentication"
    Configuration Outcome = "configuration"
    Malformed Outcome = "malformed"
    Ambiguous Outcome = "ambiguous"
    Review Outcome = "review"
    Created Outcome = "created"
    Reconciled Outcome = "reconciled"
)
// Failure contains a safe category and operation, never a remote response body.
type Failure struct { Kind Outcome; Operation string }
func (f Failure) Error() string { return f.Operation + ": " + string(f.Kind) }
// Lease loss and disallowed manual retry use errors.Is-compatible sentinels in implementation.
// Context cancellation remains context.Canceled or context.DeadlineExceeded.
type PrivateEvidence struct {
    Metadata StageMetadata
    Prompt, Schema, Output []byte
    PromptSHA256, SchemaSHA256 string
    ProviderResponseID string
}
type ExtractRequest struct { Transcript string; ProjectAliases []string }
type Extracted struct { Items []Item; Evidence PrivateEvidence }
type Decision string
const ( Accept Decision = "accept"; NeedsReview Decision = "review"; Reject Decision = "reject" )
type AssessmentRequest struct {
    Transcript string
    Item Item
    ProjectAliases []string
    Metadata StageMetadata
}
type AssessmentResult struct {
    Reason string
    Item Item
    Decision Decision
    Scores map[string]float64
    PrivateDetail []byte
    Metadata StageMetadata
}
type Frozen struct { Extraction Extracted; Assessments []AssessmentResult }
type ReconciliationStatus string
const (
    Found ReconciliationStatus = "found"
    Absent ReconciliationStatus = "absent"
    Uncertain ReconciliationStatus = "ambiguous"
)
type ReconciliationResult struct { Status ReconciliationStatus; Remote RemoteIdentity }
type DeliveryRequest struct { Identity ItemIdentity; Item Item; Marker string; TimeZone string }

// Inbound use cases. Drivers own polling, HTTP, command parsing, and serialization.
type IntakeService interface { Accept(context.Context, Intake) (Receipt, error) }
type WorkflowService interface { RunOnce(context.Context) (worked bool, err error) }
// Outbound ports belong to the application consumer.
type Extractor interface { Extract(context.Context, ExtractRequest) (Extracted, error) }
type Assessor interface { Assess(context.Context, AssessmentRequest) (AssessmentResult, error) }
type InjectionScreener interface { Screen(context.Context, string) (bool, error) }
type DeliveryProvider interface {
    Create(context.Context, DeliveryRequest) (RemoteIdentity, error)
    Reconcile(context.Context, DeliveryRequest) (ReconciliationResult, error)
}
type IntakeRepository interface { Accept(context.Context, Intake) (Receipt, error) }
type ExtractionRepository interface {
    ClaimExtraction(context.Context, Lease) (*ExtractionClaim, error)
    FreezeExtraction(context.Context, RecordingID, ExtractionFence, Frozen) error
    RetryExtraction(context.Context, RecordingID, ExtractionFence, Outcome, time.Time) error
    ReviewExtraction(context.Context, RecordingID, ExtractionFence, Outcome) error
    BlockExtraction(context.Context, RecordingID, ExtractionFence, Outcome, string) error
    CompleteRejectedExtraction(context.Context, RecordingID, ExtractionFence) error
}
type DeliveryRepository interface {
    ClaimDelivery(context.Context, Lease) (*DeliveryClaim, error)
    CompleteDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, RemoteIdentity) error
    RetryDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, time.Time) error
    RetryReconciliation(context.Context, DeliveryID, LeaseOwner, time.Time) error
    ReviewDelivery(context.Context, DeliveryID, LeaseOwner, Outcome) error
    BlockDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, string) error
}
// Private evidence write remains separate from freeze and from shadow persistence.
type ActiveVerificationEvidence struct {
    Decision Decision
    Metadata StageMetadata
    Reason string
    Detail []byte
}
type ActiveVerificationRepository interface {
    SaveActiveVerification(context.Context, RecordingID, ExtractionFence, []ActiveVerificationEvidence) error
}
// Aggregate read models contain no transcript, item content, or private evidence.
type OperationalSnapshot struct {
    Status string
    CheckedAt time.Time
    Reasons []string
    Intake IntakeSnapshot
    Worker WorkerSnapshot
    Queue QueueSnapshot
    Providers ProviderSnapshots
}
type IntakeSnapshot struct {
    RetainedUniqueRecordings, RetainedReceiveCount int
    LastReceivedAt *time.Time
    LastDuplicate bool
    LastResponseStatus int
}
type WorkerSnapshot struct {
    State string
    Stale bool
    StartedAt, HeartbeatAt, LastCycleStartedAt, LastCycleCompletedAt *time.Time
    LastCycleFailed bool
}
type QueueSnapshot struct {
    Active, Retries, BlockedAuthentication, NeedsReview, DeadLetter int
    OldestActiveAgeSeconds int64
}
type ProviderSnapshots struct { DeepSeek, TickTick, TypeSafe ProviderSnapshot }
type ProviderSnapshot struct {
    LastLatencyMilliseconds int64
    LastObservedAt *time.Time
    LastFailed bool
}
type OperationalReader interface {
    OperationalStatus(context.Context) (OperationalSnapshot, error)
}
// Recording status is private operator data, separate from dashboard summaries.
type RecordingStatus struct {
    ID RecordingID
    Fingerprint Fingerprint
    State string
    AttemptCount int
    LastOutcome Outcome
    Provider, Model, ProviderResponseID string
    UpdatedAt time.Time
    NextAttemptAt *time.Time
    Deliveries []RecordingDeliveryStatus
    Verification []ActiveVerificationEvidence
}
type RecordingDeliveryStatus struct {
    ID DeliveryID
    Index int
    Kind ItemKind
    State string
    AttemptCount int
    LastOutcome Outcome
    Marker string
    Remote RemoteIdentity
    UpdatedAt time.Time
    NextAttemptAt *time.Time
}
type RecordingLookup struct { Found bool; Status RecordingStatus }
type RecordingReader interface {
    RecordingStatus(context.Context, RecordingID) (RecordingLookup, error)
}
// Inbound parsing normalizes raw query values before calling the dashboard port.
type RecordingFilter struct { Page, PageSize int; Client, Trigger, State string }
type RecordingSummary struct {
    ID RecordingID
    RecordedAt, FirstReceivedAt time.Time
    Client, Trigger string
    AudioBytes, TranscriptionCharacters, ReceiveCount, DeliveryCount int64
    HasExtractionJob bool
    ExtractionState string
}
type RecordingPage struct { Rows []RecordingSummary; Total int64 }
type RecordingDetail struct {
    Summary RecordingSummary
    LastReceivedAt time.Time
    Provider, Model string
    CompletedDeliveries int64
    Deliveries []DashboardDelivery
}
type DashboardDelivery struct {
    ID DeliveryID
    Index int
    Kind ItemKind
    State string
    AttemptCount int
    LastOutcome Outcome
    Remote RemoteIdentity
    CompletedAt *time.Time
    UpdatedAt time.Time
}
type RecordingDetailLookup struct { Found bool; Detail RecordingDetail }
type DashboardReader interface {
    Recordings(context.Context, RecordingFilter) (RecordingPage, error)
    Recording(context.Context, RecordingID) (RecordingDetailLookup, error)
}
// Manual retry and retention keep policy time explicit at the persistence boundary.
type RecordingRetryResult struct { ID RecordingID; State string }
type DeliveryRetryResult struct { ID DeliveryID; State string }
type PurgeResult struct { Deleted int64 }
type ManualRetryService interface {
    RetryRecording(context.Context, RecordingID) (RecordingRetryResult, error)
    RetryDelivery(context.Context, DeliveryID) (DeliveryRetryResult, error)
}
type ManualRetryRepository interface {
    RetryRecording(context.Context, RecordingID, time.Time) error
    RetryDelivery(context.Context, DeliveryID, time.Time) error
}
type RetentionService interface {
    PurgeRecordings(context.Context, time.Duration) (PurgeResult, error)
    PurgeEvaluationEvidence(context.Context) (PurgeResult, error)
}
type RetentionRepository interface {
    PurgeRecordingsBefore(context.Context, time.Time) (int64, error)
    PurgeEvaluationEvidenceThrough(context.Context, time.Time) (int64, error)
}
type ShadowEvidenceRepository interface {
    SaveShadow(context.Context, RecordingID, []AssessmentResult) error
}
type Clock interface { Now() time.Time }
type Jitter interface { Delay(time.Duration) time.Duration }
```

These interfaces describe consumer contracts, not a requirement to make every inbound service an interface.
Concrete methods are sufficient when there is one consumer and no substitution need.
The provider adapter dispatches task versus note creation from `Item.Kind`; it preserves current note routing and date rules.
Do not include provider request objects, SQL transactions, HTTP clients, filesystem handles, or loggers in these values.
Private evidence remains application-owned bytes with versioned adapter encoding. It is never a log or public response value.
A later evidence extraction must preserve current persisted field names and schemas through explicit conversion tests.

## Identity and stage semantics

`RecordingID` and `DeliveryID` preserve existing local integer IDs. They are not remote IDs.
`Fingerprint` preserves the current semantic payload digest. Intake parsing and fingerprinting remain inbound responsibilities during this extraction.
`ItemIdentity` uses the fingerprint and zero-based frozen item index. Preserve item order, including mixed task and note batches.
The remote marker remains `[index01:<fingerprint>:<index>]`. A retry must not generate another identity or marker.
`RemoteIdentity` stores the provider object and project IDs as opaque values. Never use the display title as identity.

Total and cycle attempts remain distinct. Preserve reconciliation counts and recovered-lease flags.
Stage metadata records the actual request clock, time zone, model, prompt version, and build revision where available.
An absent legacy field stays absent; do not manufacture evidence for old rows.
The assessment returns a typed item with the current route selection. Active policy decides whether to freeze that item.
Shadow mode retains the selected item only as evidence. It cannot apply that selection to delivery.
Assessment scores and decisions are observations. Only explicit application policy can grant delivery authority.
Shadow mode cannot change frozen items, create a delivery, or change a completed delivery outcome.
The injection screen remains a distinct typed capability. Do not replace it with an untyped callback pipeline.

Adapters map provider failures to existing safe outcomes. Preserve authentication, configuration, malformed, retryable, and ambiguous distinctions.
Lease loss remains distinct from provider failure. A stale worker must not retry a transaction with a new owner.
Disallowed manual retry remains distinct from missing work. Public adapters retain their existing status codes and messages.
Unknown errors retain safe diagnostics and follow existing workflow handling. Never infer that an uncertain remote create did not occur.

## Transaction ownership

The persistence adapter owns begin, commit, rollback, database time comparisons, and row-count checks.
The application invokes one semantic transition per atomic unit. It never receives a transaction or supplies a callback inside one.
No remote call occurs within a persistence transaction.

| Transition | Atomic contents |
| --- | --- |
| Intake accept | Deduplication, duplicate counter, recording, extraction job, original evidence deadline, optional input evidence. |
| Extraction claim | Eligible row selection, owner, expiry, total and cycle attempt increments. No work returns `nil, nil`. |
| Extraction freeze | Active lease check, immutable extraction, ordered items and markers, attempts, optional captured evidence, workflow state, zero-item erasure. |
| Rejected extraction | Active lease check, terminal state and attempt, transcript erasure. |
| Delivery claim | Eligible row selection, active lease, attempt counters, recovered-lease and reconciliation state. |
| Delivery completion | Active lease check, remote identity, attempt, evidence, existing shadow delivery association, last-sibling transcript erasure. |
| Retry, review, block | Active lease check, constrained outcome, counters, next attempt time, state and workflow state. |
| Shadow save | Existing evidence association without overwriting delivery outcome. Preserve bounded post-cancellation persistence behavior. |

The existing methods remain authoritative until adapter extraction passes their regression tests.
Save active verification evidence before freeze as the current workflow requires. Preserve its separate lease-checked write.
Do not combine that write with shadow persistence or silently change the existing failure window.
`ActiveVerificationRepository` now specifies that separate write. The current adapter method remains `Store.SaveExtractionVerification`.
Its typed evidence list preserves original item order, including accepted, review, and rejected decisions.
`Detail` carries a versioned private encoding of current questions, answers, token counts, and other persisted evidence fields.
The assessment result carries a typed reason so the application need not decode private detail to prepare this write.
Metadata preserves model and prompt version. It must not invent clock or stage values for legacy rows.
Adapters must preserve existing persisted field names through conversion tests before extraction.
An empty evidence list is a no-op before lease validation, including a missing recording or canceled context.
A nonempty write requires the active owner, current claim attempt, and an unexpired extraction lease.
Wrong, expired, completed, or superseded attempts return lease loss, including same-owner reclaims.
The durable total attempt number is the per-claim fence. It increases on each claim and survives operator cycle resets.
All extraction transitions use this fence. No HTTP/CLI interface or database schema changes are required.
Encoding failure leaves prior evidence unchanged. Saving evidence cannot freeze items or create delivery tasks.
The write commits before review, rejected completion, or freeze. A later failed transition does not undo that evidence write.
Rejected completion erases the operational transcript but retains the already saved verification record.
That record follows existing recording retention. Do not imply erasure of every private evidence copy.

## Aggregate read and screening semantics

`OperationalReader` is the shared aggregate read for health/status drivers and aggregate dashboard display.
It returns typed values. Inbound adapters retain existing public field names, optional fields, timestamps, authentication, and status codes.
`LastResponseStatus` is existing intake response telemetry. It does not give application policy a transport client or response object.
Persistence maps absent database timestamps to `nil`. Inbound adapters preserve the current omitted-field representation.
`CheckedAt` uses the injected clock. Retain millisecond and second units in the named fields.

The read must not open a write-capable dashboard connection, run migrations, refresh evidence expiry, or update worker state.
Current service health reads use the existing service store. Dashboard opening remains a separate read-only adapter requirement.
No aggregate field may carry transcripts, titles, notes, provider request bodies, or active/shadow evidence.
Do not reuse this model for private recording detail or evidence export.
Unknown storage errors propagate as errors; adapters retain their existing safe diagnostics and unavailable responses.
Keep aggregate queries within the existing consistency behavior. This specification does not promise a cross-query snapshot transaction.

`InjectionScreener.Screen` runs before extraction when active verification supplies the capability.
A positive screen sends extraction to review without calling the extractor.
A retryable screen failure retries only while the cycle attempt limit permits another attempt.
Other screen failures, or exhausted attempts, select review. This differs from some later per-item verification failure classifications.
A screen does not produce an item evidence list and cannot grant delivery authority.
No active verification enablement or threshold change follows from these signatures.

This specification retains the PR #38 source baseline for health reason and expiry behavior.
On October 18, the formerly pending source changes were separate local commits on `cleanup/worktree-review` at `77b7594`.
Health `3411659`, reconciliation `d0ec0f8`, export fixture `a0c99d1`, and their documentation remain excluded from this stack.
The primary checkout is clean. Review and publish those contributions separately before selecting the migration behavior baseline.

## Recording and dashboard read semantics

`RecordingReader` returns private operator status. It can contain the fingerprint, delivery marker, and active verification evidence.
Do not expose this model through aggregate health responses or dashboard rendering.
Keep provider response identity and verification order compatible with current operator serialization.
An absent extraction job returns `Found=false`, including an existing audio-only recording without a job.
An existing job with no deliveries returns an empty delivery list. Delivery status follows item-index order.
Storage and evidence-decoding errors remain errors. Do not map them to a missing recording.

`DashboardReader` returns metadata and counts without transcript text, audio filenames, fingerprints, markers, or private verification detail.
Its detail model includes existing remote object/project identifiers and provider/model names for private operator display.
The inbound adapter owns HTML escaping, evidence phrases, timestamp formatting, and previous/next URLs.
The persistence adapter computes character counts with SQLite `length`; these are Unicode character counts, not UTF-8 byte counts.
`HasExtractionJob` distinguishes an erased transcript from an audio-only receipt when the character count is zero.
This distinction reports evidence presence. It does not prove that all retained private copies were erased.

The inbound adapter parses `page` and `page_size`, with defaults 1 and 25.
Invalid or out-of-range values use the defaults. Page bounds are 1 through 1,000,000; size bounds are 1 through 100.
Trim client, trigger, and state filters, then preserve the current 200-byte truncation rule.
The legacy `status` query supplies state only when the raw `state` query is empty, before trimming.
The port receives these normalized values. It does not accept URLs or query maps.
Client, trigger, and workflow-state filters combine with AND and use exact, parameterized equality.
Do not substitute extraction lease states for workflow states. A missing job has workflow state `not-started`.
List rows use descending recording ID. Total counts use the same filters before LIMIT/OFFSET pagination.
An empty page returns an empty list and the matching total, including a page beyond the last result.
Detail deliveries use ascending item index, then delivery ID. Counts retain all deliveries and completed workflow states.
An absent recording returns `Found=false`; the inbound adapter maps this result to its existing 404 response.
Audio-only recordings remain visible in dashboard detail even when `RecordingReader` reports no extraction job.
Unexpected database errors remain errors and retain the existing safe 500 response.

Both ports perform reads only. They do not refresh leases, evidence expiry, worker health, or receive counts.
Dashboard composition must use `openDashboardDatabase`, with `mode=ro`, regular-file checks, and schema validation.
It must reject symlinks, missing files, and incompatible schemas without creating files or running migrations.
Do not pass normal writable `OpenStore` to dashboard composition.
Keep GET/HEAD restrictions, loopback binding, host validation, security headers, and cost projection in inbound adapters.
These multi-query reads retain current consistency behavior. They do not promise a transaction-wide snapshot.
The future adapter maps storage absence to the explicit lookup result; `sql.ErrNoRows` must not cross the port.

## Exact-ID retry and retention semantics

`ManualRetryService` accepts one durable recording or delivery ID. The command adapter still validates positive decimal arguments.
Only `blocked_auth`, `needs_review`, and `dead_letter` can return to pending work.
Recording retry returns workflow state `received`; delivery retry returns `extracted`.
Missing IDs and all other workflow states return the errors.Is-compatible manual-retry-not-allowed sentinel.
Do not reopen completed work, active leases, frozen extraction, or scheduled automatic retries.
Storage errors and canceled contexts remain errors. A failed or disallowed retry must not change persisted rows.

The application obtains one instant from `Clock` and passes that instant to the repository.
Each repository method atomically checks the state and changes exactly the specified row.
Recording retry resets cycle attempts, next-attempt time, lease fields, last classification, and completion time.
Delivery retry also resets reconciliation attempts. It preserves an `ambiguous` classification and clears other last classifications.
The next claim must reconcile an ambiguous write before another create attempt.
Both retries preserve total attempt counts, attempt history, recording identity, item identity, payloads, and existing evidence.
Delivery retry preserves remote object/project IDs and completed siblings.
Neither retry renews the original evidence deadline, recaptures expired evidence, or changes retention configuration.
The existing total extraction attempt remains the claim fence across cycle resets.
Keep current command names, JSON field names, state values, safe error behavior, and status privacy in inbound adapters.

`RetentionService` validates positive retention before computing a cutoff from the injected clock.
The existing command still uses 30 days and its exact confirmation string. These signatures add no retention setting.
`PurgeRecordingsBefore` uses strict chronological comparisons, including the fractional part of UTC timestamps.
At the exact cutoff, retain the recording. A single later nanosecond can make otherwise eligible work old enough.
The receipt must be older than the cutoff. A recent duplicate receipt protects the recording.
An audio-only receipt with no extraction job needs no further queue-state condition.
With a job, `received`, `extracting`, and `retry_wait` prevent deletion.
The extraction job must be older than the cutoff.
Any delivery in `extracted`, `creating`, or `retry_wait` prevents deletion.
Any delivery updated at or after the cutoff also prevents deletion, including terminal deliveries.
Keep the selection and delete within the existing atomic SQL statement.
Return the deleted recording count, excluding dependent cascade rows. A repeated purge returns zero when nothing else qualifies.

Operational deletion cascades through dispatches, extraction jobs, extraction results, attempts, and delivery rows.
Evaluation evidence uses its own original deadline and is not a recording foreign-key child.
Queue retention can erase the operational row while private evaluation evidence remains retained until its own expiry.
`PurgeEvaluationEvidenceThrough` deletes evidence whose deadline is at or before the supplied instant.
Its dependent evaluation deliveries, observations, and shadow evidence follow existing foreign-key cleanup.
Keep recording purge and evidence purge as separate operations. No combined transaction or all-private-copy erasure is promised.
Adapters map the recording count to current `recordings_deleted` output and retain the current retention-days field.

The current SQLite adapter writes canonical UTC RFC3339Nano timestamps.
Whole seconds omit a fraction. Comparing the raw strings misorders `...00Z` and `...00.000000001Z`.
The bounded correction removes the trailing UTC suffix during retention comparisons, preserving full fractional precision and strict boundaries.
It leaves stored timestamps, schema, retention duration, queue guards, and evidence expiry unchanged.
The behavior regressions demonstrate both premature deletion and missed deletion in the previous query.

## Remaining boundary contracts

The core signatures above are the first bounded part of `.1.2`.
The following consumers require the stated remaining work before this task can close:

| Consumer | Required contract work | Migration owner |
| --- | --- | --- |
| Active verification evidence | Write signature and lease lifecycle specified here. Provider encoding conversion and adapter extraction remain unimplemented. | Application policy and persistence |
| Status, dashboard, and health | Aggregate, private recording, and dashboard read signatures are specified. Schema-compatible adapter conversion and extraction remain open. | Inbound interface and application policy |
| Operator retry and retention | Exact-ID retry, purge count, cutoff, and original evidence-expiry signatures are specified. Adapter extraction and CLI parity remain open. | Application policy and persistence |
| Observation and export | Versioned evidence values, chronology, read-only export selection, explicit file delivery boundary. | Application policy and persistence |
| Backup, restore, and runtime | Keep database and file safety in adapters; specify shutdown, worker health, and storage-last close ordering. | Runtime and persistence |

## Invariant ownership and validation

[application-invariants.json](application-invariants.json) records invariant IDs, owners, rules, and existing regression entry points.
`TestApplicationContractSpecification` type-checks the fenced signatures and rejects imports outside the neutral allowlist.
`TestApplicationInvariantReferences` checks that each listed test exists in the current source and each invariant has an owner.
These checks validate the specification and references. Running the referenced integration tests remains necessary.
The full offline suite exercises the current SQLite, synthetic HTTP provider, workflow, and replay implementations.
Passing that suite does not prove that future extracted adapters satisfy these contracts.

## Product decisions and pending source disposition

- Active verification enablement and thresholds remain under `index-01-hook-677.4`. No threshold changes occur here.
- New routing semantics remain under `index-01-hook-bpy`. Preserve current aliases and note routing.
- Retention policy changes remain under `index-01-hook-zzv`. Preserve original evidence expiry and current transcript-copy distinctions.
- Existing-item mutation and history policy remain under `index-01-hook-66f`.
- Source disposition for separately committed reconciliation and health-expiry changes remains under `index-01-hook-b7y.2.1`.

The last item is source disposition, not permission to select either behavior during extraction.
Do not claim this contract slice includes the pending fixes.
This slice leaves `.1.2` and its parent open until remaining signatures and validation evidence are supplied.

## Review and local validation

Five independent local reviewers inspected this bounded specification against `72217cd`.
They reviewed source semantics, guard behavior, history, scope, and invariant evidence.
Review found a missing typed assessed item, helper functions accepted as tests, and insufficient freeze ordering references.
The corrections add the item, require runnable test names and signatures, and reference mixed-item ordering and evidence rollback tests.
A source wording correction states that the guard scans current source, including unstaged source.
Three reviewers inspected the corrections and reported no remaining concrete findings within this scope.
The typed route consumer and negative guard cases passed focused tests.
A uniquely tagged Docker build and network-isolated synthetic SQLite volume, backup, and restore test passed.
Source review, local tests, hosted CI, and eventual production adapter parity remain separate evidence.

## Safe continuation

Follow [the recurring workflow](../recurring-work.md). Hold the shared clone lock through publication and issue updates.
This slice depends on the ownership baseline in PR #37. The independent dependency stack remains separate.
Preserve the primary checkout and the local architecture-plan worktree.
Continue `.1.2` with active-verification evidence and typed status/read contracts before extracting those consumers.
Re-read authoritative Beads and current PR heads before selecting the next child base.

The published core specification is [PR #38](https://github.com/klauern/index-01-hook/pull/38), based on #37.
The reviewed specification revision is `59a9636`. All six offline gates passed after the review corrections.
ShellCheck, actionlint, and diff checks also passed. Live provider checks remained disabled.
Check final-head hosted CI separately before acceptance.

## October 18 evidence and aggregate-read validation

This continuation specifies the active evidence write and aggregate operational read on parent PR #38 at `7dd99c7`.
It adds real SQLite regressions for active, wrong, expired, reclaimed, and completed lease owners.
The regressions preserve saved evidence after encoding failure and after a later rejected-completion transition fails.
They verify empty-list no-op behavior and evidence retention after successful rejected completion.
An aggregate test exercises the store and `/statusz`, rejects private sentinels, and checks that SQLite write counts do not change.
The write-count assertion relies on the store's single connection. It does not claim absence of external filesystem effects.
Compile consumers prepare typed active evidence and invoke the aggregate read without decoding provider data.
The invariant registry now has 18 entries.

Five independent local reviewers checked source semantics, guards, history, scope, and invariants against `7dd99c7`.
They reported no concrete defects. Two reviewers checked the final typed-reason and failed-transition refinements.
Source review remains separate from test evidence.
Focused regressions, all six offline gates, ShellCheck, actionlint, and diff checks passed.
A dedicated Docker image passed synthetic network-isolated volume, backup, and restore checks.
Live provider and deployment end-to-end checks were not run. Hosted CI remains a separate acceptance gate.

The remaining `.1.2` work includes recording/dashboard reads, operator retry and retention, observation/export, and runtime contracts.
The new signatures do not establish adapter conversion parity or completion on `main`.

The evidence/read child is [PR #39](https://github.com/klauern/index-01-hook/pull/39), based on #38.
Its reviewed implementation revision is `a6c4970`. Recheck final-head CI before acceptance.


## Green-and-ready baseline update

The all-PR repair adds shared prerequisite [#40](https://github.com/klauern/index-01-hook/pull/40).
It changes the compiler and builder pins to patched Go 1.26.9 without changing application behavior.
It carries Nick's scoped export-fixture fix `a0c99d1` with provenance as `daeb20b`.
Earlier exclusions of that fixture patch describe the original contract slices, before this repair.
Health `3411659`, reconciliation `d0ec0f8`, and the independent primary documentation remain preserved and outside this stack.
The [readiness policy](../pr-readiness.md) now requires green final-head checks and ready status for every PR.
This gate supersedes the earlier temporary security-baseline tolerance. It does not close the remaining contract work or merge the stack.

## Intake regression coverage

The intake invariant now enables evaluation capture during concurrent identical submissions.
It requires one recording, one extraction job, one evidence row, and the full receive count.
Injected capture and queue failures must roll back recording, job, and evidence rows together.
A retry after each injected failure must accept one complete intake without a duplicate receipt.
These regressions use synthetic input and SQLite triggers. They do not change production transaction behavior.

## Claim-attempt fencing correction

The legacy owner-and-expiry guard allowed a previous claim to write after the same owner reclaimed an expired lease.
The current store and worker now require the claimed total attempt for evidence, freeze, retry, review, block, and rejected completion.
Same-owner reclaim tests reject stale attempts without changing persisted state, then accept the current attempt.
The worker forwards its claim attempt. Existing empty-evidence no-op behavior remains unchanged.
