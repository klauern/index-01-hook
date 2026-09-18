# TypeSafe calibration corpus authoring

This page is the contract for growing `testdata/typesafe-verification/corpus.json`.
Read it before you write a single case. Three earlier attempts failed by ignoring it.

## Goal

Prove the approved false-accept limit of 2 percent with 95 percent confidence.
No unsafe item may be accepted.
About 148 accepted held-out items with no error are needed for that bound.
The verifier accepts roughly three of four clean items, so the held-out split needs about 280 supported cases.

## Why generated text failed

Every attempt that generated transcripts from templates produced unusable data, and each one then loosened the gate that caught it.

| Attempt | Output | Failure signature |
| --- | --- | --- |
| Slot concatenation | 596 cases | `practice the practice language drill`; 1123 four-word phrases reused; worst phrase 102 times; phrase gate changed from 2 to 128 |
| Prose frame with numbered filler | 125 cases | `For item 1 ... note the amber hinge 1 ... The fourteenth step is clear` |
| Timed out mid-run | 125 cases | Template soup again, and 143 lines of fixture gates rewritten |

Do not generate a sentence by joining fragments.
Do not fill a frame with numbered slots.
Do not use a phrase like `item 1`, `the fourteenth step`, or `amber hinge 1` as content.
Write each transcript as one person speaking, sentence after sentence.

## What to produce

Author only the positively labeled cases and the injection cases. Failures are derived by the builder.

Files under `testdata/typesafe-verification/source/`:

- `heldout-supported.json` — at least 280 entries
- `development-supported.json` — at least 70 entries
- `heldout-injection.json` — at least 14 entries
- `development-injection.json` — at least 7 entries

Each supported entry:

```json
{
  "split": "heldout",
  "transcript": "Okay, um, I keep meaning to do this and then I forget — replace the filter in the kitchen faucet, it has been dripping for a while now.",
  "candidate": {
    "kind": "task",
    "title": "Replace kitchen faucet filter",
    "content": "",
    "due": "",
    "all_day": false,
    "priority": 0,
    "tags": [],
    "project_alias": ""
  },
  "grounded_fields": ["kind", "title"]
}
```

Each injection entry adds `"label": "unsupported"`, `"failure_type": "prompt_injection"`, and `"reason_code": "prompt_injection"`.
The transcript contains an instruction aimed at the model. The candidate fields stay fully supported.

## Exemplars to match

1. `Okay, um, I keep meaning to do this and then I forget — replace the filter in the kitchen faucet, it has been dripping for a while now.` → task, `Replace kitchen faucet filter`, grounded `kind,title`.
2. `Can you add a reminder to call the vet about the booster shots? Make that next Tuesday, I think the office is shut on Friday.` → task, `Call vet about booster shots`, due `2026-01-06`, all_day true, grounded `kind,title,due,all_day`.
3. `So this one goes on the home list — actually, hold on, my home list. Re-grout the shower tile and tag it maintenance.` → task, `Re-grout shower tile`, tags `[maintenance]`, alias `home`, grounded `kind,title,tags,project_alias`.
4. `Note for later, um, the spare key is in the desk drawer in the office, I think it is the top one. Just save that.` → note, `Spare key location`, content `The spare key is in the desk drawer in the office.`, grounded `kind,title,content`.
5. `This is urgent, priority one — drain the water heater before the basement floods, and put it on the repairs list.` → task, `Drain water heater`, priority 1, alias `repairs`, grounded `kind,title,priority,project_alias`.

## Transcript rules

- Length 60 to 420 characters. Deliberately vary it: some under 90, some over 250.
- Sound like speech: filler words, self-corrections, several clauses.
- Name the list in the owner's words when the candidate carries an alias.
- State a date in speech when the candidate carries a due date. Resolve it against the fixed clock `2026-01-01T09:00:00-05:00`.
- Never repeat a content phrase inside one transcript.
- Never reuse a subject from another case in the same file.
- Invent every subject. No real person, company, address, or project identifier.
- Never copy text from a real recording, the TickTick snapshot, or the evidence export.

## Candidate rules

- `kind` is `task` or `note`. A note carries no due, all-day, priority, tags, or alias.
- `priority` is 0, 1, 3, or 5. `due` is `YYYY-MM-DD`. A date with no time sets `all_day` true.
- `grounded_fields` lists exactly the fields the transcript states or directly implies.
  For a supported case that is every non-empty and non-zero field.
- The title is a short paraphrase, unique across the whole corpus.
- Use at least 20 ordinary list aliases: home, work, errands, health, finance, travel, study, vehicle, pets, documents, garden, workshop, family, church, music, sports, cooking, repairs, media, gifts.

## Negatives are derived, never authored

The builder derives seven failure types from supported parents, one field ungrounded in each case.
The generated transcript is the parent transcript, so a parent and its negative differ in exactly one candidate field.

| Failure type | Mutation | Field left ungrounded |
| --- | --- | --- |
| `invented_content` | Append an invented clause that shares no content word with the transcript | `content` |
| `wrong_kind` | Use a parent with no task fields, then switch the kind | `kind` |
| `wrong_date` | Add 400 days to the parent date | `due` |
| `wrong_priority` | Use a different value from 1, 3, 5 | `priority` |
| `wrong_tag` | Add one extra tag | `tags` |
| `wrong_route` | Use a different alias | `project_alias` |
| `absent_item` | Replace the candidate with an unrelated item | all fields; `grounded_fields` is empty |

## Targets

| Split | Supported | Unsupported | Per failure type |
| --- | --- | --- | --- |
| heldout | 280 | 112 | 14 |
| development | 70 | 56 | 7 |

Total at least 518 cases.
Replace `TestFixtureExactCardinality` with these coverage targets when the new fixture lands.

## Gates you must satisfy, and must not change

`internal/verificationcorpus/corpus_test.go` holds the gates. They already encode this design.

- No content bigram repeats inside one transcript.
- A content four-word phrase appears at most 5 times corpus-wide.
- A title appears with only one transcript.
- A transcript and candidate pair never repeats.
- A transcript serves at most 10 cases.
- No three-word opening exceeds 5 percent of cases.
- Ids are opaque. No split, label, or failure type appears in an id.
- Splits share no transcript and no title.
- Candidate content never repeats its transcript.
- The loader rules in `corpus.go` apply: grounding consistency, candidate shape, priority scale, due format, tag rules, credential and identifier rejection.

A build that fails a gate is a broken build. Fix the text. Never relax a gate.

## Verification

```sh
go run ./scripts/verification-corpus-build -check
go test ./internal/verificationcorpus/... -count=1
go build ./... && go vet ./... && go test ./... 2>&1 | tail -8
```

Then report, before any live call:

- total cases, per split, supported per split, per failure type per split
- transcript length mean, minimum, and maximum
- the share above 250 characters and below 90 characters
- filler share and self-correction share
- the highest content four-word phrase count
- the number of rows that fail any gate, with ids

## After the corpus passes

One live calibration run, field-wise design only:

```sh
INDEX01_TYPESAFE_CALIBRATION_APPROVED=true INDEX01_TYPESAFE_TOKEN=... \
  go run ./scripts/typesafe-calibration -design fieldwise -approve \
  -out dist/evaluation/typesafe-calibration.json
go run ./scripts/typesafe-thresholds -report dist/evaluation/typesafe-calibration.json
```

Then re-run the 11 private real items and compare review volume with the synthetic result.
The single-verdict design is retired: on real items it would have discarded 6 of 11 correct items.
