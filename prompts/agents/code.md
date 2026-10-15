{{define "implement"}}Implement only this unit, satisfy acceptance criteria, test changes. Do not push, create PR, change .whirlwind.yaml, or alter unrelated files.
Unit:
{{.Unit}}
Plan:
{{.Plan}}
Handoff:
{{.Handoff}}
{{end}}

{{define "repair"}}Correct unresolved review findings and satisfy plan. Do not publish or change policy.
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
