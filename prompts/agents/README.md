# Workflow agent prompts

The Innovation and Worker loops use these Markdown sources:

- `ideate.md`: one Expand, Optimize, Harden, and Simplify idea from the index, GitHub sources, and full project history.
- `ideate_novelty.md`: separate semantic uniqueness review across the batch and all historical project suggestions.
- `plan.md`: implementation planning and answered questions.
- `code.md`: named `implement`, `repair`, and `integrate` templates.
- `review.md`: general review and the named `specialist` template for security and architecture reviews.
- `learning.md`: shared optional experience report, appended after role context; wraps the normal result without changing its contents.

These files contain the existing role instructions and task-context placeholders.
The provider interface sends one combined prompt; these are not separate provider-level system messages.
Go `text/template` substitutes `{{.Field}}` values without escaping or reinterpreting task content.
Keep output JSON contracts and placeholder names compatible with their workflow consumers.
The shared learning envelope is normalized before those consumers; legacy unwrapped results remain accepted.
Handoff, selected repository context, and embedding retrieval remain appended by the shared workflow code.

The Go binary embeds these files at build time. Edit Markdown and rebuild or restart
`go run ./cmd/whirlwind` to apply changes. No runtime prompt directory or working-directory dependency is required.
