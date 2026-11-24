# Whirlwind release preflight (not accepted)

This is a [frozen main baseline and integration blocker report](whirlwind-release-preflight.json),
not a release candidate. The exact revision, component versions, commands,
package outcomes, failed test names and inspected PR heads are recorded in
the JSON report. No branch was rebased, merged, closed, tagged, or deployed.

## Integration blockers

A Git pairwise dry run inspected 210 pairs of 21 independently tested PR
heads. The other 204 pairs were clean in isolation, not a combined test.
Resolve these overlaps before freezing a single candidate:

| PR pair | Conflicting path |
| --- | --- |
| [#35](https://github.com/nirmalhk7/whirlwind/pull/35), [#49](https://github.com/nirmalhk7/whirlwind/pull/49) | `scripts/dashboard_pty_support.py` |
| [#36](https://github.com/nirmalhk7/whirlwind/pull/36), [#40](https://github.com/nirmalhk7/whirlwind/pull/40) | `internal/dashboard/runs.go` |
| [#38](https://github.com/nirmalhk7/whirlwind/pull/38), [#49](https://github.com/nirmalhk7/whirlwind/pull/49) | `internal/dashboard/dashboard_input.go` |
| [#39](https://github.com/nirmalhk7/whirlwind/pull/39), [#43](https://github.com/nirmalhk7/whirlwind/pull/43) | `README.md` |
| [#40](https://github.com/nirmalhk7/whirlwind/pull/40), [#42](https://github.com/nirmalhk7/whirlwind/pull/42) | `README.md` |
| [#44](https://github.com/nirmalhk7/whirlwind/pull/44), [#48](https://github.com/nirmalhk7/whirlwind/pull/48) | `README.md` |

`git merge-tree --write-tree --messages <left-head> <right-head>` checks
two commits without updating the working tree or branch refs. Refetch the
listed heads and rerun after revisions; a changed head invalidates its old
result.

## What still needs verification

- Freeze one integrated commit after the overlapping reviews are resolved.
- Rerun the full Go race suite, vet/build, Node suites and real terminal
  human workflow on that exact commit. The Linux baseline is not green, and
  this host cannot open the private supervisor socket. Its Darwin builds are
  cross-compiles, not native acceptance.
- On supported macOS with pinned nono and Qlty, verify scanner onboarding,
  candidate identity and finding scope; run the approved configured ACP and
  native workflow checks. Missing dependencies and unrun checks stay open.
- Update the [source acceptance ledger](whirlwind-plan-acceptance.json)
  with evidence from that same final revision. Do not carry independent
  branch passes or this baseline forward as release evidence.
