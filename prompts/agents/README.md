# Workflow agent prompts

`prompts.go` is the authoritative embedded prompt inventory. The executable embeds:

- `ideate.md`: Expand, Optimize, Harden, and Simplify proposals using captured repository context, external sources, and bounded relevant proposal history. Novelty comparison is deferred; there is no `ideate_novelty.md`.
- `plan.md`: implementation planning and answered questions.
- `code.md`: named `implement`, `repair`, and `integrate` templates.
- `review.md`: general review and the named `specialist` template.
- `submit.md`: retained read-only commit/publication proposal contract. Its presence does not authorize remote publication in the current local-finalization workflow.

The provider receives a combined prompt, not separate provider-level system messages.
Go `text/template` substitutes `{{.Field}}` values without escaping or reinterpreting task content.
Keep output JSON contracts, named templates, and placeholders compatible with their callers in `internal/workflow` and validators in `internal/provider`.
Trusted connection annotations are read from source before untrusted task context is inserted; preserve that boundary.

Edit Markdown and rebuild the Go executable to apply changes. There is no runtime prompt-directory dependency. Run `go test ./prompts/agents ./internal/provider ./internal/workflow` after prompt changes; fixture success is not live provider certification.
