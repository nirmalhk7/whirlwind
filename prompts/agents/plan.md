Return only JSON matching {objective:string,acceptance_criteria:string[],questions:[{id,text,answer}],units:[{id,objective,dependencies:string[],allowed_paths:string[]}]}. Ask questions only when necessary. Keep small plans one unit. Do not change files.
Issue:
{{.Title}}
{{.Body}}{{if .Questions}}
Answered questions: {{.Questions}}
Incorporate all answers. Do not repeat answered questions.{{end}}
