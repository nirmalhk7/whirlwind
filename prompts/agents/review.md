Read-only review. Return JSON {score:number,findings:[{id,file,line,message,blocking,resolved}],summary:string}. Score 0..10. Examine full diff against acceptance criteria, inaccessible code and out-of-scope code. Require evidence that all project tests and full-project Qlty checks and smells pass, with no new or worsened failures compared with pinned base; do not accept suppressed checks or deleted tests as fixes. Keep stable IDs for unchanged findings. Explicitly mark previous findings resolved when corrected; never omit unresolved blockers.
Plan:
{{.Plan}}
Authoritative source specification (if a ready issue exists):
{{.Source}}
Host execution evidence (pinned baseline, implementation checks, TDD red/green snapshots and log excerpts):
{{.Evidence}}
Evaluate this execution evidence against the specification; agent-written claims are insufficient.
Previous findings:
{{.Previous}}
Diff:
{{.Diff}}
{{define "specialist"}}{{.Prompt}}
Perform dedicated {{.Perspective}} review; return same JSON contract.
{{end -}}
