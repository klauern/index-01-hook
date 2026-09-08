# Routing feedback from list moves

The owner treats manual list moves as corrections to the original routing by default.
For each unambiguous detected move, use the destination project as the preferred route without requiring routine manual review.
Record this as an inference from the owner policy. Keep an override for ownership changes, temporary reorganization, and other exceptions.

## Identity and evidence

Use the stable TickTick task ID to compare successive observations.
Use the exact Index 01 delivery marker to check that the observations describe the same recording item.
Use project IDs to detect list moves. A changed project name with the same ID is a list rename.

Keep these values separate:

- The originally delivered project, when the application database provides it.
- The previously observed project.
- The currently observed project.
- The expected project inferred from the owner policy or set by an explicit override.

A current destination cannot establish the original destination.
A project change does not establish who moved the task. The owner policy supplies the default interpretation of the move.
A moved item provides evidence about that item. It does not establish a routing rule for unrelated items.

## Compare private snapshots

The comparison command reads private JSON snapshots and writes a private feedback ledger.
It has no network access or TickTick mutation operation.

```sh
task eval-routing-feedback \
  PREVIOUS=/private/path/earlier-ticktick-candidates.json \
  CURRENT=/private/path/later-ticktick-candidates.json \
  OUTPUT=/private/path/routing-feedback.json
```

Use the corpus snapshot format produced by the historical TickTick collection.
Each snapshot includes its collection time, collection failures, task IDs, exact markers, and observed current item fields.
The first comparison uses the earlier snapshot as `PREVIOUS`.
For later comparisons, use the prior feedback ledger as `PREVIOUS` to preserve the observation sequence and existing feedback.
Use a new output filename for each run. The command does not overwrite inputs or existing outputs.

The ledger keeps trusted item observations under `observations`, move history under `events`, and current lookup results under `findings`.
Each new move event retains both complete item observations and sets `expected_route.project_id` to the destination project ID.
Its review status is `inferred`, its basis is `owner_default`, and its interpretation is `original_routing_error`.
This status does not claim that the owner individually reviewed the event.
The command does not infer a project alias from the list name.
Prior review annotations remain available when the next comparison uses the ledger as its previous input.
Existing event annotations, including older pending events, are not relabeled by this default.
Recovered transcripts, prompt context, and original delivery evidence also remain available when later snapshots contain missing or unavailable fields.
Each trusted observation keeps its last known item modification time separately from the raw snapshot.
Missing timestamps cannot erase this bound. A later stale response requires review instead of creating a reverse correction.
Older ledgers recover this bound from retained observations and move history when that evidence exists.

The first snapshot is the baseline for future observations.
It cannot reveal moves that occurred before that snapshot unless original delivery evidence is available.
To obtain another snapshot, repeat the read-only TickTick collection and retain the same format and stable identifiers.

## Expected results

| Observation | Result |
| --- | --- |
| Same task ID and marker; different project ID | Record a routing correction with the destination as the preferred project. |
| Same project ID; changed project name | Record no move. |
| Same task and project in another poll | Record no duplicate correction. |
| Work to Home, then Home to Work | Preserve both transitions. |
| Task absent from the current snapshot | Mark it not observed. Do not infer deletion or completion. |
| Failed collection or incomplete evidence | Keep the uncertainty visible. Do not invent a destination. |
| Duplicate task IDs or conflicting markers | Require review. Do not choose a match by title. |
| Changed title or content with stable identity | Preserve the observed edits as context for review. |
| Unknown original destination | Leave the original destination unknown. |
| Newer poll contains an older item modification timestamp | Require review and preserve the trusted observation. |

## Use corrections in evaluations

Use the inferred destination as the expected routing label by default.
Review exceptions when a move reflects a later ownership change or temporary reorganization.
The expected project remains separate from the observed project so an override does not alter the evidence.

For an accepted correction, annotate the move event like this:

```json
{
  "review": {
    "status": "approved",
    "reason": "The original task belonged in the Home list."
  },
  "expected_route": {
    "project_id": "synthetic-home-project",
    "project_alias": "home"
  }
}
```

For an ownership change or temporary reorganization, mark the event rejected for routing evaluation and record the reason.
If the item moves again, the new event receives the new destination as its preferred route. Earlier annotations remain intact.
When building a corpus, select the latest applicable preference for each item and exclude rejected events.
These annotations support review. The command does not automatically train a model or promote annotations into production behavior.

If the original transcript is available, pair it with the inferred or explicitly overridden expected project.
If the original transcript is missing, keep the historical example blocked. The owner requires original transcripts for historical prompt evaluation.

Use [the corpus converter and runner](feedback-corpus.md) to turn the ledger into versioned evaluation input and audit its coverage.
Add synthetic feedback cases to `testdata/evaluation/feedback-corpus.json` and run `task eval`.
Use explicit opt-in live trials to measure whether the model selects the expected project from the transcript.
Offline saved-output checks validate the parsing, routing, and scoring contract.

Include related examples that should use other projects when evaluating a proposed preference.
Keep sibling items from one recording in the same development or held-out split.
Do not use the same reviewed example both to select a prompt change and to claim independent improvement.

The feedback ledger does not change the production prompt.
Automatic use of preferences in production requires a separate routing policy, bounded examples, and evaluation evidence.
