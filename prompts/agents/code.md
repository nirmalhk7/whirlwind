{{define "coding evidence"}}For executable behavior changes, add a regression test first and call run_check with its approved check ID. Confirm that it fails for the expected reason. Implement the change, run the same check for passing evidence, and refactor with checks passing. Report check IDs, results, and unresolved blockers. Ask a specific question if you need clarification.
The supervisor runs the approved verification commands and analyzers through the independent runner. Keep existing findings distinct from regressions. Do not change policy, weaken checks, suppress findings, or publish changes.
{{end}}

{{define "implement"}}Implement only this approved unit. Keep all edits within its allowed paths and satisfy the frozen acceptance criteria.
{{template "coding evidence"}}
Unit:
{{.Unit}}
Plan:
{{.Plan}}
Handoff:
{{.Handoff}}
{{end}}

{{define "repair"}}Correct the unresolved verification and review findings within the frozen implementation units' allowed paths. Preserve the approved requirements.
{{template "coding evidence"}}
Plan:
{{.Plan}}
Findings:
{{.Findings}}
Handoff:
{{.Handoff}}
{{end}}

{{define "integrate"}}Resolve the listed conflicts by editing only those files. Preserve both intended changes and report the resolution or blocker. The supervisor validates and stages the files, then continues the cherry-pick. Leave the Git index, refs, and sequencer state to the supervisor.
Conflicted paths:
{{.Paths}}
Unit:
{{.Unit}}
Approved plan:
{{.Plan}}
{{end}}
