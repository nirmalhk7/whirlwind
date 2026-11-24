# Whirlwind Plan evidence ledger

The [source ledger](whirlwind-plan-acceptance.json) preserves the read-only
Page capture at sequence 3035 and older checkpoint records. Its old
`status`, mapped commands and local log names are historical. Use the
[current evidence map](whirlwind-plan-current-evidence.json) for assessment.
All 141 requirement rows retain their original block ID and hash; section
titles are not acceptance criteria.

Evidence entries are named fixtures passed on the exact baseline, passing
checks on **unmerged** PR heads, or historical context only. An individual
PR report never qualifies `main`, another PR, or the final product. The
linked PR has that branch's exact commands and coverage limits. Every
in-scope row names what still needs to happen and its issue numbers.
Deferred rows assert no implementation; partially deferred or waived rows
describe what is excluded. Linux remains inspection-only.

Check ledger structure and, optionally, the named Go outcomes:

```sh
python3 scripts/validate-plan-evidence.py
python3 scripts/validate-plan-evidence.py --go-race-json /path/to/baseline-race.jsonl
```

Collect that optional JSON with `go test -race -json` on the recorded baseline,
selecting the exact test names in `main_fixture_pass` entries. Node and PR
results are reported separately. Passing the validator is not a test run or
release certification.

Release acceptance requires one frozen integrated revision after applicable
repairs. Record new regression, native scanner, configured ACP, and real
terminal evidence for that exact head. This ledger intentionally records
`not_accepted`; do not use it to close release verification.
