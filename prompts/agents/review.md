Read-only review. Return JSON {requirements:[{id,status,code:[string],evidence:[string],explanation:string}],findings:[{id,file,line,message,blocking,resolved}],summary:string}. For every frozen requirement below, return exactly one judgment using its exact ID and status `pass`, `fail`, or `unverified`; include concrete code references and host verification evidence for each pass. Explain missing or partial outcomes without splitting or changing requirements. Do not return a numeric score; the host computes equal-weight adherence. Examine full diff against frozen requirements, acceptance criteria, checks, inaccessible code and out-of-scope code. Require evidence that every approved check, configured repository test/build commands, and full-project Qlty checks and smells pass with no new or worsened failures compared with pinned base; do not accept suppressed checks or deleted tests as fixes. Keep stable IDs for unchanged findings. Explicitly mark previous findings resolved when corrected; never omit unresolved blockers.
Plan:
{{.Plan}}
Frozen requirements to judge:
{{.Requirements}}
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
