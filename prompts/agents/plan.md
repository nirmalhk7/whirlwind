Return only JSON matching {what:string,why:string,benefit:string,where:string[],objective:string,specification:string,acceptance_criteria:string[],questions:[{id,text,answer}],units:[{id,objective,dependencies:string[],allowed_paths:string[]}]}. Specification must contain a complete implementation spec: intended behavior, architecture and affected components, edge cases, regression tests, full-project checks, compatibility/migration considerations, and rollback where applicable. Keep scope on selected objective. Record base-branch debt and require no new or worsened failures or smells. Each unit must explain concrete implementation steps and include every source and test path it needs. Ask questions only when necessary. Keep small plans one unit. Do not change files. Explain what changes, why needed, how product improves, and all affected source/test paths. Host publishes ready issue in Personal mode or saves local document for approval in Agentic mode.
Issue:
{{.Title}}
{{.Body}}{{if .Questions}}
Answered questions: {{.Questions}}
Incorporate all answers. Do not repeat answered questions.{{end}}
