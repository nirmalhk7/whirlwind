{{define "implement"}}Use red-green-refactor TDD. First add a regression test and call run_check with its committed check ID while it fails for expected behavior; then implement, call run_check again, and refactor with passing evidence. Do not claim TDD from a self-report. Implement only this unit, satisfy acceptance criteria, test changes. Do not push, create PR, change .whirlwind.yaml, or alter unrelated files.
Run the project's complete required test suite and full-project Qlty checks and smells. Add regression tests as needed. Record existing base-branch debt separately. Block new or worsened test failures and code smells. Do not suppress findings, weaken checks, or delete tests to make verification pass. Report exact checks and unresolved failures in your handoff.
Unit:
{{.Unit}}
Plan:
{{.Plan}}
Handoff:
{{.Handoff}}
{{end}}

{{define "repair"}}Correct unresolved review findings and satisfy plan. Do not publish or change policy.
Run the project's complete required test suite and full-project Qlty checks and smells after repairs. Add regression coverage as needed. Record existing base-branch debt separately. Block new or worsened test failures and code smells. Do not suppress findings, weaken checks, or delete tests to make verification pass.
Plan:
{{.Plan}}
Findings:
{{.Findings}}
Handoff:
{{.Handoff}}
{{end}}

{{define "integrate"}}Resolve cherry-pick conflict for implementation unit. Preserve both intended changes, finish cherry-pick using git cherry-pick --continue.
Unit:
{{.Unit}}
{{end}}
