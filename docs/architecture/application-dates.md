# Application date policy

Beads `index-01-hook-b7y.3.2` owns the date-value and due-parser layer.
This independent date chain starts at application values PR47. Identity PR48 is not a prerequisite.
Use a separate native stack with PR47's branch as its base. Do not change stack49's existing membership or bases.

The application package owns DateParts and ParseDue. TypeSafeDateParts remains a root compatibility alias.
The production DeepSeek adapter consumes ParseDue and maps safe errors to its existing malformed category and operation.
Date-only values use an explicit location. Timed values retain their supplied offset and must match that location at the represented instant.
Missing dates, malformed values and invalid offsets retain their existing outcomes. Errors do not retain supplied date text.
No date parser reads a clock. Provider prompts, verification settings, schema and stored due dates remain unchanged.

Run application date tables, provider error compatibility and worker/SQLite retry-and-reopen regressions before full offline validation.
Use Go1.26.10, pinned builders, lint, manifests, vulnerability and secret scans. Verify all exact-head hosted checks after readiness.
Live providers and private calibration remain outside these synthetic checks.
Record exact heads, source coverage, review limits and final hosted checks in Beads.
The dependent layer owns semantic date resolution with explicit time and location.
