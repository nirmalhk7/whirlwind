# Seeded terminal acceptance

Run the human-decision fixture with Python 3 and Vim installed:

```sh
WHIRLWIND_TEST_SEEDED_PTY=1 go test -race ./cmd/whirlwind -run '^TestSeededHumanWorkflowThroughPTY$' -count=1 -timeout=2m
```

This opens the shipped `./bin/whirlwind dashboard` in two real terminals. The
fixture owns temporary Git and SQLite state and serves the production command
adapter on a private Unix socket. It checks draft edits, stale decisions,
separate approvals, Planner cancellation, Vim saves, pending clarification
across reopen, and inspection of stored delivery evidence.

Role responses and delivery evidence are seeded. The separate
`python3 scripts/dashboard-pty-acceptance.py` check covers starting, reopening,
and stopping the supervisor process.
