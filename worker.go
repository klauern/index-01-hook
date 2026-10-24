package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

const (
	shadowVerificationConcurrency = 4
	shadowPersistenceTimeout      = 10 * time.Second
)

type ExtractionProvider interface {
	Extract(context.Context, string, []string) (FrozenExtraction, error)
}

type DeliveryProvider interface {
	CreateTask(context.Context, string, TickTickTaskInput) (TickTickCreatedTask, error)
	CreateNote(context.Context, TickTickNoteInput) (TickTickCreatedTask, error)
	ReconcileItem(context.Context, TickTickReconciliationInput) (TickTickReconciliationResult, error)
}
type WorkerConfig struct {
	EvidenceRouting       *evalcorpus.RoutingConfig
	Verifier              ExtractionVerifier
	ShadowVerifier        ExtractionVerifier
	Owner                 string
	TimeZone              string
	LeaseDuration         time.Duration
	PollInterval          time.Duration
	RetryBase             time.Duration
	RetryMaximum          time.Duration
	ExtractionMaxAttempts int
	DeliveryMaxAttempts   int
	ReconcileMaxAttempts  int
	ProjectAliases        []string
	Jitter                func(time.Duration) time.Duration
	Logger                *slog.Logger
}

type Worker struct {
	store       *Store
	extractor   ExtractionProvider
	deliverer   DeliveryProvider
	config      WorkerConfig
	shadow      sync.WaitGroup
	shadowSlots chan struct{}
}

func NewWorker(store *Store, extractor ExtractionProvider, deliverer DeliveryProvider, config WorkerConfig) (*Worker, error) {
	if store == nil || extractor == nil || deliverer == nil {
		return nil, fmt.Errorf("store and providers are required")
	}
	if config.Verifier != nil && config.ShadowVerifier != nil {
		return nil, fmt.Errorf("active and shadow verification cannot both be configured")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, fmt.Errorf("worker owner is required")
	}
	config.TimeZone = strings.TrimSpace(config.TimeZone)
	if config.TimeZone == "" {
		config.TimeZone = defaultDeepSeekTimeZone
	}
	normalizedTimeZone, err := normalizeTimeZone(config.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("worker time zone is invalid")
	}
	config.TimeZone = normalizedTimeZone
	if config.LeaseDuration <= 0 || config.PollInterval <= 0 || config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase {
		return nil, fmt.Errorf("worker durations are invalid")
	}
	if config.ExtractionMaxAttempts <= 0 || config.DeliveryMaxAttempts <= 0 || config.ReconcileMaxAttempts <= 0 {
		return nil, fmt.Errorf("worker attempt limits are invalid")
	}
	if config.Jitter == nil {
		return nil, fmt.Errorf("worker jitter is required")
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("worker logger is required")
	}
	aliases, err := normalizeProjectAliases(config.ProjectAliases)
	if err != nil {
		return nil, fmt.Errorf("worker project aliases are invalid")
	}
	config.ProjectAliases = aliases
	return &Worker{
		store:       store,
		extractor:   extractor,
		deliverer:   deliverer,
		config:      config,
		shadowSlots: make(chan struct{}, shadowVerificationConcurrency),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := w.store.WorkerStarted(ctx, w.config.Owner); err != nil {
		return err
	}
	defer func() {
		if err := w.store.WorkerStopped(context.WithoutCancel(ctx), w.config.Owner); err != nil {
			w.config.Logger.Warn("worker health update failed")
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		worked, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			w.config.Logger.Error("worker cycle failed")
		}
		if ctx.Err() != nil {
			return nil
		}
		delay := w.config.PollInterval
		if worked {
			delay = 0
		}
		timer.Reset(delay)
	}
}

func (w *Worker) RunOnce(ctx context.Context) (worked bool, cycleErr error) {
	if err := w.store.WorkerCycleStarted(ctx, w.config.Owner); err != nil {
		w.config.Logger.Warn("worker health update failed")
	}
	defer func() {
		if err := w.store.WorkerCycleCompleted(context.WithoutCancel(ctx), w.config.Owner, cycleErr != nil); err != nil {
			w.config.Logger.Warn("worker health update failed")
		}
	}()
	claim, err := w.store.ClaimExtraction(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil {
		return false, err
	}
	if claim != nil {
		return true, w.processExtraction(ctx, claim)
	}
	delivery, err := w.store.ClaimDelivery(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil {
		return false, err
	}
	if delivery != nil {
		return true, w.processDelivery(ctx, delivery)
	}
	return false, nil
}

// processExtraction carries the claimed attempt through every extraction write and transition.
func (w *Worker) processExtraction(ctx context.Context, claim *ExtractionClaim) error {
	started := time.Now()
	if screener, ok := w.config.Verifier.(ExtractionSecurityScreener); ok {
		screenStarted := time.Now()
		injected, screenErr := screener.Screen(ctx, claim.Transcription)
		w.recordProviderLatency(ctx, "typesafe", time.Since(screenStarted), screenErr != nil)
		if screenErr != nil {
			if classifyTypeSafeError(screenErr) == TypeSafeErrorRetryable && claim.CycleAttemptNumber < w.config.ExtractionMaxAttempts {
				return w.store.RetryExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
			}
			return w.store.ReviewExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeReview)
		}
		if injected {
			return w.store.ReviewExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeReview)
		}
	}
	extraction, err := w.extractor.Extract(ctx, claim.Transcription, w.config.ProjectAliases)
	w.recordProviderLatency(ctx, "deepseek", time.Since(started), err != nil)
	if err == nil {
		shadowItems := append([]QueuedItem(nil), frozenItems(extraction)...)
		if w.config.Verifier != nil && len(extraction.Items) > 0 {
			accepted := make([]QueuedItem, 0, len(extraction.Items))
			verifications := make([]TypeSafeVerificationEvidence, 0, len(extraction.Items))
			needsReview := false
			for _, item := range extraction.Items {
				verificationStarted := time.Now()
				result, verificationErr := w.config.Verifier.Verify(ctx, claim.Transcription, item, w.config.ProjectAliases)
				w.recordProviderLatency(ctx, "typesafe", time.Since(verificationStarted), verificationErr != nil)
				if verificationErr != nil {
					if classifyTypeSafeError(verificationErr) == TypeSafeErrorRetryable {
						if claim.CycleAttemptNumber >= w.config.ExtractionMaxAttempts {
							return w.store.ReviewExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeRetryable)
						}
						return w.store.RetryExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
					}
					return w.store.ReviewExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeReview)
				}
				verifications = append(verifications, result.Evidence)
				switch result.Decision {
				case VerificationAccept:
					accepted = append(accepted, result.Item)
				case VerificationReview:
					needsReview = true
				case VerificationReject:
				default:
					needsReview = true
				}
			}
			if err := w.store.SaveExtractionVerification(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, verifications); err != nil {
				return err
			}
			if needsReview {
				return w.store.ReviewExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeReview)
			}
			if len(accepted) == 0 {
				return w.store.CompleteRejectedExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber)
			}
			extraction.Items = accepted
			extraction.Verifications = verifications
		}
		if extraction.Evidence != nil && w.config.EvidenceRouting != nil {
			routing := *w.config.EvidenceRouting
			routing.Clock = extraction.Evidence.Clock.Format(time.RFC3339Nano)
			routing.TimeZone = extraction.Evidence.TimeZone
			routing.Aliases = make(map[string]string, len(w.config.EvidenceRouting.Aliases))
			for alias, project := range w.config.EvidenceRouting.Aliases {
				routing.Aliases[alias] = project
			}
			extraction.Evidence.Routing = &routing
		}
		if err := w.store.FreezeExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, extraction); err != nil {
			return err
		}
		if w.config.ShadowVerifier != nil {
			w.startShadowVerification(ctx, claim.RecordingID, claim.Transcription, shadowItems)
		}
		return nil
	}
	w.logDeepSeekFailure(claim.RecordingID, err)
	kind := classifyDeepSeekError(err)
	switch kind {
	case DeepSeekErrorAuthentication:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeAuthentication, "blocked_auth")
	case DeepSeekErrorReview:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeReview, "needs_review")
	case DeepSeekErrorMalformed, DeepSeekErrorTerminal:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeMalformed, "dead_letter")
	case DeepSeekErrorRetryable:
		if claim.CycleAttemptNumber >= w.config.ExtractionMaxAttempts {
			return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeRetryable, "dead_letter")
		}
		return w.store.RetryExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
	default:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, claim.AttemptNumber, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) WaitForShadow() {
	w.shadow.Wait()
}

func (w *Worker) startShadowVerification(ctx context.Context, recordingID int64, transcription string, items []QueuedItem) {
	select {
	case w.shadowSlots <- struct{}{}:
	case <-ctx.Done():
		return
	default:
		w.config.Logger.Warn("typesafe shadow verification skipped because concurrency limit is full", "recording_id", recordingID)
		return
	}
	w.shadow.Add(1)
	go func() {
		defer w.shadow.Done()
		defer func() { <-w.shadowSlots }()
		w.runShadowVerification(ctx, recordingID, transcription, items)
	}()
}

func (w *Worker) runShadowVerification(ctx context.Context, recordingID int64, transcription string, items []QueuedItem) {
	results := make([]ShadowVerification, 0, len(items))
	shadowModel := ""
	if modelProvider, ok := w.config.ShadowVerifier.(VerificationModelProvider); ok {
		shadowModel = modelProvider.VerificationModel()
	}
	for index, item := range items {
		started := time.Now()
		result, verificationErr := w.config.ShadowVerifier.Verify(ctx, transcription, item, w.config.ProjectAliases)
		shadow := ShadowVerification{
			ItemIndex: index, Model: shadowModel, PromptVersion: typeSafeVerificationPromptVersion,
			Outcome: "queued", LatencyMilliseconds: time.Since(started).Milliseconds(),
		}
		if verificationErr != nil {
			shadow.Decision = "error"
			shadow.Error = verificationErr.Error()
			shadow.ErrorKind = classifyTypeSafeError(verificationErr)
		} else if result.Decision != VerificationAccept && result.Decision != VerificationReview && result.Decision != VerificationReject {
			shadow.Decision = "error"
			shadow.Error = "shadow verifier returned an invalid decision"
			shadow.ErrorKind = TypeSafeErrorMalformed
		} else {
			shadow.Evidence = &result.Evidence
			shadow.Decision = string(result.Decision)
			shadow.InputTokens = result.Evidence.InputTokens
			if result.Evidence.Model != "" {
				shadow.Model = result.Evidence.Model
			}
			shadow.Scores = typeSafeAnswerScores(result.Evidence.Answers)
		}
		results = append(results, shadow)
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shadowPersistenceTimeout)
	defer cancel()
	if err := w.store.SaveShadowVerifications(persistCtx, recordingID, results); err != nil {
		w.config.Logger.Warn("typesafe shadow evidence persistence failed", "recording_id", recordingID)
	}
}

func (w *Worker) logDeepSeekFailure(recordingID int64, err error) {
	kind := classifyDeepSeekError(err)
	attributes := []any{
		"recording_id", recordingID,
		"classification", kind,
		"reason_code", deepSeekFailureReason(err),
	}
	if failure, ok := application.FailureOf(err); ok && failure.StatusCode != 0 {
		attributes = append(attributes, "status_code", failure.StatusCode)
	}
	w.config.Logger.Warn("deepseek extraction failed", attributes...)
}

// deepSeekFailureReason accepts only existing static reason codes from the neutral failure port.
func deepSeekFailureReason(err error) string {
	failure, ok := application.FailureOf(err)
	if !ok {
		return "unexpected_error"
	}
	switch failure.Reason {
	case "client_validation", "item_validation", "provider_authentication", "provider_http_error", "provider_refusal", "provider_terminal", "provider_transport", "response_envelope", "structured_output", "unexpected_error", "unknown_provider_error":
		return failure.Reason
	default:
		return "unexpected_error"
	}
}

func (w *Worker) processDelivery(ctx context.Context, claim *DeliveryClaim) error {
	if claim.Kind != ItemKindTask && claim.Kind != ItemKindNote {
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
	if claim.RecoveredLease || claim.LastClassification == OutcomeAmbiguous {
		return w.reconcileDelivery(ctx, claim)
	}
	var created TickTickCreatedTask
	var err error
	started := time.Now()
	switch claim.Kind {
	case ItemKindTask:
		created, err = w.deliverer.CreateTask(ctx, claim.ProjectAlias, TickTickTaskInput{
			Title: claim.Title, Content: claim.Notes, RecordingHash: claim.RecordingHash,
			TaskIndex: claim.TaskIndex, Priority: claim.Priority, Tags: claim.Tags,
			Due: claim.Due, AllDay: claim.AllDay, TimeZone: w.config.TimeZone,
		})
	case ItemKindNote:
		created, err = w.deliverer.CreateNote(ctx, TickTickNoteInput{
			Title: claim.Title, Content: claim.Notes, RecordingHash: claim.RecordingHash,
			TaskIndex: claim.TaskIndex,
		})
	}
	w.recordProviderLatency(ctx, "ticktick", time.Since(started), err != nil)
	if err == nil {
		return w.store.CompleteDelivery(ctx, DeliveryCompletion{
			TaskID: claim.ID, LeaseOwner: w.config.Owner, Classification: OutcomeCreated,
			TickTickTaskID: created.ID, TickTickProjectID: created.ProjectID,
		})
	}
	return w.handleDeliveryError(ctx, claim, err)
}

// reconcileDelivery confirms the original remote item or preserves ambiguity without another create.
func (w *Worker) reconcileDelivery(ctx context.Context, claim *DeliveryClaim) error {
	started := time.Now()
	result, err := w.deliverer.ReconcileItem(ctx, TickTickReconciliationInput{
		Kind: claim.Kind, ProjectAlias: claim.ProjectAlias, Marker: claim.Marker,
		Title: claim.Title, Content: claim.Notes,
	})
	w.recordProviderLatency(ctx, "ticktick", time.Since(started), err != nil)
	if err != nil {
		return w.handleReconciliationError(ctx, claim, err)
	}
	switch result.Status {
	case TickTickReconciliationConfirmed:
		if strings.TrimSpace(result.TaskID) == "" || strings.TrimSpace(result.ProjectID) == "" {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
		}
		return w.store.CompleteDelivery(ctx, DeliveryCompletion{
			TaskID: claim.ID, LeaseOwner: w.config.Owner, Classification: OutcomeReconciled,
			TickTickTaskID: result.TaskID, TickTickProjectID: result.ProjectID,
		})
	case TickTickReconciliationReview:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
	case TickTickReconciliationUnconfirmed:
		// A missing marker does not prove that an ambiguous create failed.
		// Never issue another POST from this state.
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) handleReconciliationError(ctx context.Context, claim *DeliveryClaim, err error) error {
	kind := classifyTickTickError(err)
	switch kind {
	case TickTickErrorAuthentication:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "blocked_auth")
	case TickTickErrorConfiguration, TickTickErrorMalformed:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
	case TickTickErrorRetryable, TickTickErrorAmbiguous:
		if claim.ReconcileAttemptCount+1 >= w.config.ReconcileMaxAttempts {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
		}
		return w.store.RetryDeliveryReconciliation(ctx, claim.ID, w.config.Owner, w.retryAt(claim.ReconcileAttemptCount+1))
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeReview, "needs_review")
	}
}

func (w *Worker) recordProviderLatency(ctx context.Context, provider string, duration time.Duration, failed bool) {
	if err := w.store.RecordProviderLatency(context.WithoutCancel(ctx), w.config.Owner, provider, duration, failed); err != nil {
		w.config.Logger.Warn("worker health update failed")
	}
}

func (w *Worker) handleDeliveryError(ctx context.Context, claim *DeliveryClaim, err error) error {
	kind := classifyTickTickError(err)
	switch kind {
	case TickTickErrorAuthentication:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAuthentication, "blocked_auth")
	case TickTickErrorConfiguration:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeConfiguration, "needs_review")
	case TickTickErrorMalformed:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	case TickTickErrorAmbiguous:
		return w.store.RetryDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, w.retryAt(claim.CycleAttemptNumber))
	case TickTickErrorRetryable:
		if claim.CycleAttemptNumber >= w.config.DeliveryMaxAttempts {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeRetryable, "dead_letter")
		}
		return w.store.RetryDelivery(ctx, claim.ID, w.config.Owner, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) retryAt(attempt int) time.Time {
	delay := w.config.RetryBase
	for index := 1; index < attempt && delay < w.config.RetryMaximum; index++ {
		if delay > w.config.RetryMaximum/2 {
			delay = w.config.RetryMaximum
			break
		}
		delay *= 2
	}
	if delay > w.config.RetryMaximum {
		delay = w.config.RetryMaximum
	}
	jitterLimit := delay / 4
	if remaining := w.config.RetryMaximum - delay; jitterLimit > remaining {
		jitterLimit = remaining
	}
	jitter := w.config.Jitter(jitterLimit)
	if jitter < 0 || jitter > jitterLimit {
		jitter = 0
	}
	return w.store.now().UTC().Add(delay + jitter)
}

// classifyDeepSeekError preserves the root helper while consuming neutral failure metadata.
func classifyDeepSeekError(err error) DeepSeekErrorKind {
	if failure, ok := application.FailureOf(err); ok {
		if failure.Kind.Valid() {
			return DeepSeekErrorKind(failure.Kind)
		}
		return ""
	}
	return DeepSeekErrorMalformed
}

// classifyTickTickError preserves the root helper while consuming neutral failure metadata.
func classifyTickTickError(err error) TickTickErrorKind {
	if failure, ok := application.FailureOf(err); ok {
		if failure.Kind.Valid() {
			return TickTickErrorKind(failure.Kind)
		}
		return ""
	}
	return TickTickErrorMalformed
}
