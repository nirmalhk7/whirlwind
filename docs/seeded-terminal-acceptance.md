# Seeded terminal acceptance

Run the human-decision fixture with Python 3 and Vim installed:

```sh
WHIRLWIND_TEST_SEEDED_PTY=1 go test -race ./cmd/whirlwind -run '^TestSeededHumanWorkflowThroughPTY$' -count=1 -timeout=2m
```

This opens two real terminals with temporary Git/SQLite state. It drives the
production dashboard and supervisor dispatcher in-process to test draft
edits, stale decisions, separate approvals, Planner cancellation, Vim saves,
pending clarification across reopen, and inspection of stored delivery evidence.
It does not start providers or open a Unix socket. Run
`python3 scripts/dashboard-pty-acceptance.py` separately on a host that
supports Unix sockets to check the shipped launcher and supervisor lifecycle.
