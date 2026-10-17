# ADR-0002: Application contracts and migration invariants

Date: 2026-10-17

Status: Selected contract specification. Production extraction is not implemented.

Scope: `index-01-hook-b7y.1.2`, following the [boundary inventory](../architecture/boundary-inventory.md).
The source baseline is PR #37 at `72217cd`. Pending primary-checkout changes remain excluded.
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
    FreezeExtraction(context.Context, RecordingID, LeaseOwner, Frozen) error
    RetryExtraction(context.Context, RecordingID, LeaseOwner, Outcome, time.Time) error
    ReviewExtraction(context.Context, RecordingID, LeaseOwner, Outcome) error
    BlockExtraction(context.Context, RecordingID, LeaseOwner, Outcome, string) error
    CompleteRejectedExtraction(context.Context, RecordingID, LeaseOwner) error
}
type DeliveryRepository interface {
    ClaimDelivery(context.Context, Lease) (*DeliveryClaim, error)
    CompleteDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, RemoteIdentity) error
    RetryDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, time.Time) error
    RetryReconciliation(context.Context, DeliveryID, LeaseOwner, time.Time) error
    ReviewDelivery(context.Context, DeliveryID, LeaseOwner, Outcome) error
    BlockDelivery(context.Context, DeliveryID, LeaseOwner, Outcome, string) error
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
The core port list will need a separate active-verification evidence write before that path is extracted.
This is a declared follow-up contract, not an implemented parity claim.

## Remaining boundary contracts

The core signatures above are the first bounded part of `.1.2`.
The following consumers need separate neutral signatures before this task can close:

| Consumer | Required contract work | Migration owner |
| --- | --- | --- |
| Active verification evidence | Lease-checked evidence save, screen failure mapping, and rejected extraction evidence lifecycle. | Application policy and persistence |
| Status, dashboard, and health | Typed redacted snapshots, schema-compatible reads, no migrations, current health reasons. | Inbound interface and application policy |
| Operator retry and retention | Exact-ID retry restrictions, purge counts, original evidence expiry, no product retention changes. | Application policy and persistence |
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
- Pending moved-item reconciliation and health-expiry changes remain under `index-01-hook-b7y.2.1`.

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
