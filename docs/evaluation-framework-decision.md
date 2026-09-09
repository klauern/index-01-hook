# Evaluation framework decision

Date: 2026-09-07. Epic: `index-01-hook-x6e`.

Adopt Go testing with `go-cmp` v0.7.0 as the evaluation core.
Use versioned JSON fixtures and separate local JSON reports. Adopt no additional reporting layer now.
Keep current behavior and proposed behavior at equal priority with separate contracts and results.

The baseline implements the required structural checks without an evaluation framework dependency.
It reuses production extraction, routing, and worker test interfaces.
Experimental actions remain in test files and simulated state.
The only new Go module is `go-cmp`, used by tests.

## Evidence and scope

The [contract](evaluation.md) defines the fixture format, scoring rules, commands, policies, and report fields.
Saved reports are in [evaluation-baseline](evaluation-baseline/).
They contain 17 current passes, including four delivery checks, and 15 proposed passes.
These passes measure scripted adapter and scorer behavior. They do not measure live model accuracy.
Semantic grading remains explicitly unsupported.

The baseline required a contract/scorer file, a runner file, a scorer test file, a JSON fixture set,
two Task commands, and these documents. Existing production code required no changes.
Most custom work implements state transitions, strict input validation, provenance, and negative scorer tests.
Those responsibilities remain necessary with each reviewed framework.
Implementation effort was observed in this local implementation; no candidate integration time was measured.

Candidate assessments use pinned source and documentation. No candidate prototype or hosted upload was needed.
There is no runtime report benchmark between candidates.
If a later prototype is needed, feed it the saved baseline observations and compare status counts before using fresh model outputs.

## Candidate results

| Candidate | Result | Reason |
| --- | --- | --- |
| Go testing and `go-cmp` v0.7.0 | Adopt | Direct production adapter access, strict repository-owned scoring, readable differences, and no reporting service requirement. |
| `igcodinap/go-eval` at `d50759ac637cebfcf70fa3053c24d729d9b4c071` | Defer | Strong Go integration and local reports; explicit error, skip, and unsupported accounting still needs integration work. |
| `liliang-cn/eval-go` at `7fac31c8872969765a595a7604c837ba5ad908b0` | Reject for this core | Built-in agent metrics lose required distinctions and can pass missing evidence. Replacing those metrics removes much of the benefit. |
| Braintrust Go SDK v0.12.0 at `e05d23b32184c16499e4f3475b6e31d716bfd232` | Defer optional reporting | Hosted experiment review may help later. Beta APIs, export verification, and service integration exceed current report needs. |
| Promptfoo v0.119.13 at `d1419964849e897b61e3871af8d009fc217be93e` | Defer optional local viewer | Side-by-side review and import/export are useful, but require a second runtime and a result-format adapter. |

## go-eval assessment

Reviewed sources:

- [Case and structured artifacts](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/case.go)
- [Contract checks](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/contract.go)
- [Runner and result emission](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/eval.go)
- [Repeat](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/repeat.go)
- [JSON, Markdown, and HTML reports](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/compare/report.go)
- [Result fields](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/metric.go)
- [Module requirements](https://github.com/igcodinap/go-eval/blob/d50759ac637cebfcf70fa3053c24d729d9b4c071/go.mod)

Both suites can map actions and state into `Case.Artifacts`, with suite and trial information in metadata.
Custom metrics can call the repository scorer. `Contract` retains a failed boolean when any child check fails.
The core requires Go 1.22 and uses the standard library. Judge adapters use separate modules.
Existing structured artifacts and local reports make this the strongest deferred candidate.

`Repeat.Score` calls `m.Metric.Score(ctx, j, c)` repeatedly with the same case.
It repeats scoring or judge calls. It does not generate new model outputs.
Fresh generation would still belong in the hook adapter and trial loop.

The reviewed `Result` has `Passed`, numeric scores, dimensions, and metadata, but no five-state status field.
`Runner.Run` skips before result emission when disabled or filtered.
It also calls `Fatalf` on a metric error before the ordinary result sink path.
A complete report must therefore combine metric results with Go test events or add a status-preserving adapter.
Do not count only emitted metric rows as all attempted cases.

Adoption would retain our fixture validation, transition checks, forbidden-action rules, and trial policy.
It would add result mapping, test-event reconciliation, and upstream API maintenance.
Defer until local run history or interactive comparison becomes a measured need.
No source uncertainty required a prototype for this decision.

## eval-go assessment

Reviewed sources:

- [Tool and argument metrics](https://github.com/liliang-cn/eval-go/blob/7fac31c8872969765a595a7604c837ba5ad908b0/metrics_agent.go)
- [Judge parsing](https://github.com/liliang-cn/eval-go/blob/7fac31c8872969765a595a7604c837ba5ad908b0/metrics_judge.go)
- [Sample, Result, and aggregation](https://github.com/liliang-cn/eval-go/blob/7fac31c8872969765a595a7604c837ba5ad908b0/eval.go)
- [JSON reporting](https://github.com/liliang-cn/eval-go/blob/7fac31c8872969765a595a7604c837ba5ad908b0/report.go)
- [Module requirements](https://github.com/liliang-cn/eval-go/blob/7fac31c8872969765a595a7604c837ba5ad908b0/go.mod)

The following source paths establish incompatible behavior without model calls:

| Input | Reviewed built-in behavior | Required hook behavior |
| --- | --- | --- |
| Expected `create`; actual `create`, `create` | `ToolCorrectness` converts names to sets and passes. | Fail the duplicate action count. |
| Expected no tools; actual `close` | The empty expected-tools branch returns `Passed: true`, score 1, with a skipped reason. | Fail the unexpected closure. |
| Expected `close(task-a)`; actual `close(task-b)` | Tool-name comparison ignores arguments and passes. | Fail the wrong target deterministically. |
| One tool call; judge returns `{}` | `ArgumentCorrectness` treats the missing verdict as correct and scores 1. | Return an error for missing required evidence. |
| Missing rubric | `RubricJudge` returns a pass with score 1. | Keep unsupported or missing checks outside pass counts. |

The built-in `Result` distinguishes metric errors through `Err`, but represents skip reasons as passing metric results in these paths.
The hook would need replacement tool scoring, argument scoring, judge validation, and status aggregation.
The same replacements apply to current multi-item extraction and proposed action sequences.
Required guards already fail or return errors in the repository scorer tests.

The core package describes itself as standard-library-only.
The module manifest also includes Cobra and the `agent-go/v3` adapter dependency; importing only core avoids compiling the adapter.
The module graph still adds version and maintenance considerations.
JSON reporting alone does not justify these replacements. Reject this candidate for the selected core.

## Braintrust assessment

Reviewed sources:

- [v0.12.0 release](https://github.com/braintrustdata/braintrust-sdk-go/releases/tag/v0.12.0)
- [Beta status and setup](https://github.com/braintrustdata/braintrust-sdk-go/blob/e05d23b32184c16499e4f3475b6e31d716bfd232/README.md)
- [Cases, trials, errors, and experiment execution](https://github.com/braintrustdata/braintrust-sdk-go/blob/e05d23b32184c16499e4f3475b6e31d716bfd232/eval/eval.go)
- [Scores and score metadata](https://github.com/braintrustdata/braintrust-sdk-go/blob/e05d23b32184c16499e4f3475b6e31d716bfd232/eval/scorers.go)
- [Dataset reading](https://github.com/braintrustdata/braintrust-sdk-go/blob/e05d23b32184c16499e4f3475b6e31d716bfd232/eval/dataset_api.go)
- [Module requirements](https://github.com/braintrustdata/braintrust-sdk-go/blob/e05d23b32184c16499e4f3475b6e31d716bfd232/go.mod)

The README explicitly marks the SDK beta and warns that APIs may change.
The reviewed module requires Go 1.25, selects a Go 1.26.1 toolchain, and includes OpenTelemetry, OTLP, and related dependencies.
Those dependencies are unnecessary for the local baseline.

An optional reporting adapter would map each saved observation to a case output and the expected actions/state to the expected value.
Use separate experiment names for current and proposed suites.
Record case, adapter, source hashes, split, trial, clock, and status in metadata.
For `pass` and `fail`, emit structural scores of 1 and 0.
For `error`, `skip`, and `unsupported`, preserve status metadata and omit the corresponding numeric score.
Never map missing evidence to score 1. Keep forbidden-action failure counts in metadata and a separate score.
Replay saved outputs with one reporting trial; do not multiply model trials during upload.

Experiment history, trace inspection, and shared review are the potential benefits.
Account access and the hosted review workflow were not tested or requested.
The SDK supports dataset iteration, but this assessment did not verify a complete experiment export and round-trip import.
Keep local JSON as the canonical result archive. Verify status preservation and export completeness before adoption.

No credentials, transcripts, or TickTick data were uploaded. No authentication lookup was needed for this source-based assessment.
Any later integration must consume saved synthetic results and remain optional.
Defer until shared review justifies hosted data handling, authentication, dependency updates, and export validation.

## Promptfoo assessment

Reviewed sources:

- [Pinned version and Node requirement](https://github.com/promptfoo/promptfoo/blob/d1419964849e897b61e3871af8d009fc217be93e/package.json)
- [Local viewer](https://github.com/promptfoo/promptfoo/blob/d1419964849e897b61e3871af8d009fc217be93e/site/docs/usage/web-ui.md)
- [CLI imports, exports, and saved model outputs](https://github.com/promptfoo/promptfoo/blob/d1419964849e897b61e3871af8d009fc217be93e/site/docs/usage/command-line.md)
- [Custom providers](https://github.com/promptfoo/promptfoo/blob/d1419964849e897b61e3871af8d009fc217be93e/site/docs/providers/custom-api.md)

Version 0.119.13 requires Node 20 or newer.
The CLI documents JSON import/export, multiple output formats, a local viewer, and `--model-outputs` for saved outputs.
These features could fill the baseline's side-by-side review gap without changing the Go runner.
An adapter must still map the hook's five statuses, forbidden failures, suite identity, and trial provenance.
Source review does not establish that its native JSON importer accepts our report schema.

Defer the viewer until repeated human comparison warrants the second runtime and mapping maintenance.
No Promptfoo service was started. No sharing or hosted upload occurred.

## Decision limits

The selected baseline has no interactive viewer, cross-run database, semantic judge, or live experimental model adapter.
Exact text assertions can reject acceptable paraphrases during live trials.
No live model stability, accuracy, cost, or latency claim follows from the offline passes.
Live trial commands, budget constraints, and unresolved product policies are documented in the contract.

Follow-up work must define experimental policies before adding a model adapter or production mutations.
Semantic quality requires labeled synthetic examples and explicit review criteria.
Optional reporting remains deferred; it is not a required dependency of the selected core.

Tracked follow-up work:

- `index-01-hook-66f`: Define product policies for history and item mutations.
- `index-01-hook-9bj`: Calibrate semantic grading with synthetic model trials.
- `index-01-hook-2tk`: Build an isolated model adapter for proposed behavior after `index-01-hook-66f`.

## Framework update on 2026-09-08

The selected Go core now includes [a shared routing corpus, importer, and runner](feedback-corpus.md).
The importer consumes historical snapshots or list-move ledgers and preserves label provenance and recording groups.
The runner supports coverage audits, independent saved predictions, and explicitly enabled live trials.
Five new synthetic feedback cases pass through production decoding and routing.
An import-to-replay check verifies that old destinations fail after correction and preferred destinations pass.

The private historical corpus contains 44 recording groups and 45 items.
It currently has no eligible prompt trials because original transcripts and other required evidence are missing.
Those rows report unsupported rather than passing.
The update keeps the Go core and local reporting decision unchanged.
