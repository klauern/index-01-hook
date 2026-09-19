# Evaluation contract

The evaluation core uses Go tests, `go-cmp` v0.7.0, and repository-owned JSON fixtures.
The scorers and runners in `evaluation_*_test.go` are test-only.
`internal/evalcorpus` provides shared corpus validation and import logic for tests, helper commands, and application exports.
The helper commands in `scripts/eval-corpus` and `scripts/routing-feedback` build separate binaries.
The application includes evidence capture, retention, collection, and export code.
See [the framework decision](evaluation-framework-decision.md) for source evidence and selection reasons.

## Run the suites

Run the offline baseline:

```sh
task eval
```

The command disables live evaluation, runs scorer checks, and writes separate reports:

- `dist/evaluation/current-offline.json`
- `dist/evaluation/proposed-offline.json`

Run only one suite with Go subtest selection:

```sh
go test -run '^TestEvaluationOffline/current$' -count=1 -v .
go test -run '^TestEvaluationOffline/proposed$' -count=1 -v .
```

The current suite has 13 extraction scenarios and four existing delivery checks.
The proposed suite has 15 stateful scenarios.
Both suites have equal evaluation priority. Each suite has its own report and status counts.
Top-level counts summarize scenario and delivery-check outcomes. Semantic check status remains separate in each scenario result.

`task eval` also runs the [feedback corpus suite](feedback-corpus.md).
That suite consumes a versioned routing corpus and checks preferred destinations separately from the original behavior contracts.
Private historical corpora use explicit converter, audit, replay, and live commands. Missing originals remain unsupported.

The offline results test adapters and scoring contracts against saved outputs.
They do not measure a model's ability to produce those outputs.
Proposed suite failures identify experimental contract or harness failures. They do not establish a production behavior requirement.

## Fixture contract version 1

`testdata/evaluation/scenarios.json` contains one JSON array for both suites.
The loader rejects unknown fields, trailing data, duplicate case identifiers, and missing required scoring collections.
Empty collections must use `[]`. A missing or `null` collection is not an empty result.
Item scheduling defaults are empty due date, `false` all-day, priority zero, empty tags, and `false` closed.

| Field | Meaning |
| --- | --- |
| `version`, `id`, `split` | Contract version, unique case identifier, and development or held-out label. |
| `suite`, `adapter` | `current` / `production-v1`, or `proposed` / `scripted-state-v1`. |
| `clock`, `time_zone`, `aliases` | Fixed processing clock, named time zone, and synthetic project aliases. |
| `history_limit`, `history` | Number of visible messages, including the current message, and preceding messages. |
| `initial` | Initial simulated item state. Production extraction requires an empty state. |
| `turns` | Ordered messages and independent saved adapter outputs. Each message has an identifier and recording time. |
| `turns[].remote_state` | Optional authoritative item snapshot before that turn. An empty array means no remote items. |
| `expected_actions` | Exact ordered actions, including step, operation, target, item, and changed fields. |
| `expected_state` | Complete final item state. Item order does not matter; duplicate identifiers are invalid. |
| `expected_visible` | Exact visible message identifiers for each turn. |
| `forbidden` | Operation and target pairs. Target `*` matches every target. |
| `live` | Inclusion in the bounded production live subset. |

The expected actions must produce the expected state under the supplied snapshots.
An expected action cannot also be forbidden.
The loader rejects invalid expected targets, operations, item kinds, dates, and action order.

Items use symbolic identifiers. A current creation receives `s<step>-i<position>`.
Task destinations are `inbox`, `home`, or `work`; note destinations are `notes` in the supplied cases.
These names identify simulated projects. They are not real account identifiers.

The production adapter calls `DeepSeekClient.Extract` through a fixture transport.
It validates routing through `TickTickClient.ValidateRouting` using a local project response.
It then calls `TickTickRouter.ResolveItemProject` and records the resulting creation actions.
It never sends a TickTick request over the network.

The experimental adapter reads saved action arrays and applies them to an in-memory item list.
Supported operations are `create`, `update`, `close`, `review`, and `no_action`.
Updates support title, content, route, and due date. Other fields require a future adapter version.
The adapter records a bounded message window and applies supplied remote snapshots separately.
It does not infer intent, implement a history-aware prompt, or choose targets from natural language.

## Scoring

The scorer uses exact structural checks. It preserves action order and item counts.
It compares actions, replays each action, compares final state, and checks visible messages.
The scorer retains each failed check in `failure_count` and `diagnostics`.
The count measures failed checks, not distinct user mistakes or a weighted quality score.
One defect can fail more than one check.

A forbidden mutation always fails the scenario, including when a later operation returns an error or skip.
There is no aggregate score that can offset a forbidden closure.

| Status | Meaning |
| --- | --- |
| `pass` | All required structural checks passed. |
| `fail` | An observed action or state violated the contract. |
| `error` | Required input was absent, the fixture was invalid, or an adapter could not complete. |
| `skip` | Execution did not occur, for example after an earlier provider error. |
| `unsupported` | The adapter or check does not implement the requested capability. |

Missing observations do not pass. Empty action arrays are valid only when explicitly present.
The semantic check is always `unsupported` in this baseline.
Exact title or content differences detect changed text, but do not establish a semantic error.
Acceptable paraphrases can fail exact matching. Invented details require separate semantic review.

Scorer tests deliberately introduce extra items, missing items, wrong routes, wrong kinds, duplicate actions,
duplicate state, wrong dates, wrong targets, forbidden closures, invalid order, and incorrect history windows.
Other tests cover invalid fixtures, missing observations, state inconsistency, status counts, and live budgets.

Delivery checks reuse the existing replay, restart, ambiguous-note reconciliation, and bounded-retry tests.
Reports store these assertion results under `delivery_checks`, separate from extraction observations.
Their source file, source hash, fixed clock, adapter, and trial identify the configuration.
The original Go assertions remain the detailed evidence for these checks.

## Product assumptions

| Topic | Baseline contract | Unresolved product decision and required evidence |
| --- | --- | --- |
| Current task and note classification | Saved task and note outputs follow the production schema. | Measure classification and meaning with fresh synthetic model outputs and human labels. |
| Current routing | Unclear task routes use Inbox. Notes use the configured note project. | Measure semantic alias selection separately from deterministic routing. |
| Current dates | Relative dates use processing time, as the production prompt does. Recording time stays in the fixture. | Decide whether delayed recordings should use recording time; test midnight and daylight-saving boundaries. |
| Proposed message history | Ten visible messages is one experimental setting. Cases cover nine, ten, and eleven total messages. | Compare window sizes on labeled interleaved topics. Ten is not an approved production limit. |
| Proposed item history | Initial items and remote snapshots represent current item state. Message history is separate. | Define item-history fields, freshness, permissions, and conflict handling before a model adapter uses real state. |
| Proposed ambiguity | `review` or `no_action` preserves state in specified cases. | Decide how asynchronous review is surfaced and when conservative creation is preferable. |
| Proposed closure | Only one explicit synthetic task closure is accepted; other scenarios forbid closure. | Define eligible targets and a false-closure threshold before any automatic closure feature. |
| Duplicates | Exact delivery replay has existing production checks. A proposed semantic duplicate requests review. | Define when similar tasks should merge, remain separate, or require review. |
| Retention | History exists only for one test invocation and contains synthetic messages. | Define retention duration, removal behavior, and consent before storing production history. |

These proposed policies are hypotheses. The epic does not add production history, updates, or closures.
The held-out label separates case groups for later comparisons. These cases were visible during harness development.
Do not describe this baseline as a blinded or statistically representative model evaluation.

## Live trials

Live evaluation was not enabled for the baseline assessment. No paid model calls were made.
Credential availability was not inspected. A missing credential is an explicit error when a live run is requested.

The opt-in subset contains two synthetic cases: task fallback and note fallback.
To run two fresh trials per case, provide the existing token through the environment and run:

```sh
task eval-live TRIALS=2 CALL_BUDGET=4
```

The runner validates the full call count before its first provider call.
It accepts one to ten trials and at most 30 total calls. It makes no automatic retries.
After a provider error, it marks remaining trials skipped.
Every trial invokes production `Extract` again. There is no application result cache.
Provider-side prefix caching is not controlled; it does not replace fresh model generation.

`current-live.json` records fixture and prompt/schema source hashes, adapter, suite, case, split, trial,
clock, time zone, aliases, model, output-token limit, call count, latency, response identifiers, and available numeric usage.
Absent usage or provider model identifiers remain empty, not zero-valued claims about the provider.
Temperature and other unconfigured provider settings retain provider defaults; those defaults are not pinned.
The source hash covers `deepseek.go`, including the prompt, schema, model default, and decoding logic.
Judge repetitions stay zero and are separate from model generations.

## TypeSafe verification calibration

The TypeSafe spike uses only `testdata/evaluation/scenarios.json`, `testdata/evaluation/feedback-corpus.json`, and fabricated title or content negatives. The data is synthetic and small. The spike report records supported and fabricated unsupported probabilities, separation, review volume, latency, and token usage.

The spike is a provisional signal. It does not set production thresholds. Thresholds must come from the labeled evidence in `index-01-hook-9bj` and must report agreement, false-accept rate, and review rate. Cookbook values are illustrative and are not approved defaults. Use the pinned model identifier in the report. Repeat the run after a model change.

The recorded trial is `docs/evaluation-baseline/typesafe-spike.json`. It used 58 candidates, including 29 fabricated negatives. The mean unsupported probability was 0.508 for supported items and 0.792 for fabricated items. Separation was 0.284, but review volume was 82.8%. This exceeds the operable review volume. Do not enable the verification stage.
Use saved outputs when comparing reporting frameworks. Use new model generations when comparing model or prompt behavior.
Do not use repeated scoring of the same output as evidence of model stability.

### Labeled calibration corpus

The corpus path is `testdata/typesafe-verification/corpus.json`.
The corpus holds 86 cases: 43 development and 43 held-out, 19 supported cases per split, and 3 cases per failure type per split.
Each case uses the `supported` or `unsupported` label.
Each case lists `grounded_fields`. These are the candidate fields that the transcript states or directly implies.
The loader enforces the label rules. It does not judge meaning. A human reviewer must confirm that each grounded field is truly stated.
Supported cases ground every information-bearing field. A field-specific negative leaves exactly one named failure field ungrounded. `absent_item` grounds no fields.
The loader rejects a grounded field that the candidate does not carry. It also rejects a note that carries task fields, a priority outside 0, 1, 3, and 5, an all-day task without a due date, a malformed due date, and repeated or empty tags.
Prompt-injection cases keep all candidate fields grounded. Route them to review. Never route them to automatic acceptance.
`ExpectedDecision` in package `verificationcorpus` reports accept, review, or reject for a case. The calibration runner and the threshold selector must use it.
Development and held-out cases use distinct items, distinct transcripts, and different wording. No transcript or transcript-title pair appears in both splits.
Case identifiers are opaque. They do not name the split, the label, or the failure type.
The corpus is synthetic only. It contains no real recording, no credential, and no TickTick project identifier.

### Recorded calibration

The recorded calibration is `docs/evaluation-baseline/typesafe-calibration.json`.
It used 86 synthetic cases and 204 live requests to model `jev-1.13.0` with prompt version `verification-calibration-v1`.
The run made no provider error, and the mean latency was about 190 milliseconds per request.
Both designs separated supported items from unsupported items on the held-out split of 43 cases.
The field-wise design accepted 14 items with no unsafe accept and rejected 21, at accept 0.84 and reject 0.58. Review volume was 18.6 percent.
The Choice design accepted 17 items with no unsafe accept and rejected 22, at accept 0.62 and reject 0.58. Review volume was 9.3 percent.
Injected items scored 0.98 on the injection question. The highest score on a normal case was 0.80.
Repeated cases were stable. The largest score spread was 0.12, and seven of eight cases stayed within 0.05.
The selected thresholds are in `docs/evaluation-baseline/typesafe-thresholds.json`.
The `enable` values in that file are grid results only. The owner decision is defer, so neither design is adopted.

### Decision: defer

The held-out sample cannot prove the approved limits.
No unsafe item was accepted, but only 14 items were accepted by the field-wise design and 17 by the Choice design.
With no error, those counts bound the true false-accept rate at 19 percent and 16 percent with 95 percent confidence.
The approved limit is 2 percent.
The review-volume limit is also unproven: the field-wise bound is 31 percent against a limit of 25 percent.
About 148 accepted held-out items with no error are needed to bound the false-accept rate at 2 percent.
Therefore verification stays disabled, and no production threshold changes.
Collect more evidence in one of two ways: a shadow run that logs each decision and routes nothing, or a larger held-out corpus.
A full repeat run costs about 204 requests.

### Real-item transfer check

The real items stay outside the repository. The check keeps scores and counts only.
The check used the 11 tasks the hook created whose transcript was still retained, together with the owner's later state: 9 kept unchanged and 2 moved to another list.
The labels are weak. An owner keeping an item is not proof that the item is correct, so the check cannot measure false accepts and does not prove the safety limit.

The field-wise design transfers in ranking. Its mean support score was 0.83 on the real items against 0.82 on the synthetic held-out items, and it rejected no real item.
The Choice design does not transfer. Its mean score fell to 0.58 from 0.69, and it would have rejected 6 of the 11 real items. Do not adopt the Choice design.
Review volume does not transfer. At the synthetic-calibrated accept threshold of 0.84, 6 of the 11 real items would reach review. That is 55 percent against a budget of 25 percent.
Real transcripts are longer, use filler words, and carry several clauses. The synthetic corpus must include that shape before any threshold is trusted.
Keep the 11 real items as a transfer check for every later calibration run.

## Maintenance

Use [routing feedback from list moves](routing-feedback.md) to infer preferred destinations under the owner policy, with overrides for exceptions.

Update expected outputs only after reviewing the behavior change.
Run scorer mutation tests before accepting a new scoring rule.
Bump the fixture or adapter version when its meaning changes.
Keep semantic grading and experimental product decisions outside current production acceptance gates.
Keep baseline snapshots under `docs/evaluation-baseline/`; generated working reports belong in `dist/evaluation/`.
