# First production application values

Beads `index-01-hook-b7y.3.1` records this bounded part of application extraction.
The immediate parent selects the preserved source baseline before package moves.

`internal/application` now owns ItemKind, Item, Fingerprint, and ItemIdentity.
The root ItemKind and QueuedItem names are aliases. Existing adapters use the canonical values without importing the executable.
Legacy QueuedTask remains an adapter compatibility representation. Its conversion is later migration work.
JSON field order, names, kinds, due dates, priorities, tags, aliases, persistence, and root build remain unchanged.
The existing marker encoder consumes ItemIdentity; provider marker bytes remain in the root adapter.

The dependency regression rejects SQL, HTTP, and outward repository imports from the application graph.
The source inventory assigns the new production file to application policy.
A wire golden and round trip protect the compatibility aliases.
Full existing provider, queue, retry, retention, evidence, and runtime regressions remain required.

This slice does not extract use cases, date policy, validation limits, typed provider failures, or consumed ports.
Those acceptance criteria remain open. No routing, schema, active verification, private corpus, or credential policy changes.
The next dependent slice moves identity normalization and validation into this application boundary.
