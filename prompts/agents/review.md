Read-only review. Return JSON {score:number,findings:[{id,file,line,message,blocking,resolved}],summary:string}. Score 0..10. Examine full diff against acceptance criteria, inaccessible code and out-of-scope code. Keep stable IDs for unchanged findings. Explicitly mark previous findings resolved when corrected; never omit unresolved blockers.
Plan:
{{.Plan}}
Previous findings:
{{.Previous}}
Diff:
{{.Diff}}
{{define "specialist"}}{{.Prompt}}
Perform dedicated {{.Perspective}} review; return same JSON contract.
{{end -}}
