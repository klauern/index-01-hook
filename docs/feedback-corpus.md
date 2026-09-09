# Evaluate a routing feedback corpus

The routing evaluation framework consumes versioned corpus files.
The corpus can come from a private TickTick snapshot, a routing-feedback ledger, or repository-owned synthetic examples.
The same runner audits readiness, evaluates independent saved outputs, and supports explicitly enabled live trials.

This workflow does not change the production prompt, train a model, or modify TickTick items.

## Sources and labels

Import the collected snapshot to retain historical candidates and show missing evidence.
Import a later routing-feedback ledger to include list corrections and explicit overrides.
The importer preserves a hash of its source and groups sibling items by recording fingerprint.
Items from one recording share one deterministic development or held-out split.
Eligibility also requires the original extraction's expected item count.
Contiguous observed indices do not prove completeness: a search may omit the final item from a recording.
Recover or explicitly verify `historical_delivery.expected_item_count` in the source before treating the recording as complete.
The importer does not set this count from the number of currently visible TickTick items.

A current TickTick destination is an observation, not an expected routing label.
A valid latest move event supplies an inferred preferred destination under the owner policy.
An approved override can supply an explicit expected destination.
Rejected, pending, stale, incomplete, or ambiguous evidence cannot become a passing routing example.

The owner requires original transcripts for historical prompt evaluation.
The importer does not reconstruct transcripts from task titles or contents.
Examples without original transcripts stay in the corpus with explicit blocking reasons.
Synthetic examples remain clearly labeled synthetic.

## Build a corpus

```sh
task eval-corpus-build \
  INPUT=/private/path/routing-feedback.json \
  OUTPUT=/private/path/routing-corpus.json
```

The command accepts either a snapshot or ledger.
It creates a new private file and refuses to overwrite an existing file.
It does not fetch services or print task contents.

Supply `CONFIG=/private/path/routing-config.json` to choose a replay configuration:

```json
{
  "clock": "2026-09-08T09:00:00-05:00",
  "time_zone": "America/Chicago",
  "aliases": {
    "home": "synthetic-home-project",
    "work": "synthetic-work-project"
  },
  "default_project_id": "synthetic-inbox",
  "note_project_id": "synthetic-notes-project"
}
```

Replace the synthetic IDs with IDs represented by the private corpus when evaluating real examples.
This configuration selects the clock and routes to evaluate. It does not reproduce missing historical configuration.
Expected projects outside the configuration remain unsupported instead of being silently mapped to Inbox.

## Audit coverage

```sh
task eval-corpus \
  CORPUS=/private/path/routing-corpus.json \
  REPORT_DIR=/private/path/reports
```

The audit writes `feedback-audit.json` without model calls.
It reports each recording group, input provenance, label evidence, and readiness problems.
Ready examples are skipped because no prediction was evaluated.
Blocked examples are unsupported. Audit mode does not report model-quality passes.

The `coverage` object counts each recording once, independent of trial counts.
It reports readiness, saved-output availability, input provenance, label status, and blockers.
Development and held-out counts are separate. Blocker counts can overlap.
`replay_ready` means an eligible example has saved output. It does not mean the output is correct.

Use the audit to find missing evidence before changing prompts.
Current task destinations alone cannot show that a prompt made a routing error.
Require original transcripts, expected routes, complete recording groups, and routing configuration for historical evaluation.
Use development examples to revise prompts. Reserve held-out examples for checking those revisions.
Compare model-generated predictions under the same configuration before and after a prompt change.
Saved-output replay tests parsing, routing, and scoring; it does not test a revised prompt.

To unblock a historical example, recover its original transcript and complete item count, resolve its expected routes, and supply the explicit routing configuration.
Rebuild the corpus from the enriched source so its hash and label evidence remain traceable.

Update the corresponding candidate in the source JSON when you recover evidence:

```json
{
  "input": {
    "transcript": "The recovered original recording text.",
    "transcript_provenance": "original"
  },
  "historical_delivery": {
    "expected_item_count": 1
  },
  "review": {
    "status": "approved",
    "expected_project": "verified-project-id"
  }
}
```

Merge these fields into the existing candidate. Preserve its identifiers and other evidence.
In a snapshot, candidates are under `candidates`. In a ledger, they are under `observations[].candidate`.
The converter reads JSON. Edits in the separate review worksheet must first be applied to the matching JSON candidate.
An inferred move label already supplies its expected project; an explicit approved override is optional.

## Evaluate saved predictions

Replay fails when zero examples execute. Use audit mode to inspect a corpus that remains blocked.
The synthetic fixture uses a separate Inbox default so explicit Home routing cannot pass through fallback.

```sh
task eval-corpus-replay \
  CORPUS=/private/path/routing-corpus-with-predictions.json \
  REPORT_DIR=/private/path/reports
```

Each example's `saved_output` contains an independently captured DeepSeek response payload with its `items` array.
The runner sends that payload through the production decoder and router using local fixture transports.
It compares the resulting destinations with corpus labels and writes `feedback-replay.json`.

Do not construct a saved prediction by copying the expected route.
An example without a saved prediction does not pass replay evaluation.
Wrong destinations, missing items, extra items, duplicate items, and wrong kinds fail structural checks.
The runner uses normalized title and kind to associate predictions with expected items.
An acceptable title paraphrase can therefore fail strict matching. This suite does not claim semantic grading.

`task eval` includes a synthetic feedback corpus in addition to the original behavior suites.
It clears private corpus and live-corpus settings so the standard command stays offline and reproducible.
Synthetic tests verify that a correction label changes the result for the same saved prediction.
They also cover overrides, sibling outputs, and unrelated examples that should keep their existing route.
The synthetic source digest hashes the literal version label `index01-routing-feedback-synthetic-v1`.
The report separately hashes the complete corpus file, so it identifies the exact cases and predictions used.

## Evaluate original inputs with the model

```sh
task eval-corpus-live \
  CORPUS=/private/path/routing-corpus.json \
  REPORT_DIR=/private/path/reports \
  TRIALS=2 CALL_BUDGET=4
```

Set `INDEX01_DEEPSEEK_TOKEN` through the environment before running this command.
Choose a budget that covers eligible recording groups multiplied by the trial count.
The runner validates eligibility and budget before making calls. An entirely blocked corpus makes no model call.
Each eligible trial makes a fresh extraction request. Provider errors stop remaining live trials.
TickTick lookup and routing use local fixture transports in every mode.

The live report records configuration, source hashes, prompt source hash, trials, latency, available usage, and input provenance.
Reports distinguish passes, failures, errors, skips, and unsupported examples.
Missing inputs and labels cannot inflate pass counts.

Live mode transmits eligible corpus transcripts to the configured DeepSeek provider.
It is a separate explicit command. Normal evaluation and audit commands never enable it.

## Private data and corpus maintenance

Keep raw historical corpus files and reports outside the repository.
Use synthetic or sanitized examples in `testdata/evaluation/feedback-corpus.json`.
Preserve original, synthetic, and missing input provenance.
Do not replace missing originals with synthetic text while retaining a historical provenance label.
Keep collection gaps visible; a TickTick search cannot recover deleted items or zero-item extractions.
Regenerate coverage reports when transcripts, labels, routing configuration, or source observations change.
