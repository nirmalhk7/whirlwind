# Agent contribution instructions

Read the affected implementation and nearby tests before editing.
Preserve features and sandbox boundaries; update internal callers together.
Do not add migrations or compatibility shims for internal refactors.
Reuse existing domain definitions and package helpers; keep changes scoped to their owner.
Run `make fmt-check architecture-check`, affected tests, and `make -k check`.
Report the actual changes, validation results, baseline failures, and unverified platform paths.
