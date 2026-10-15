Generate exactly four useful, project-specific ideas: one per category below. Use the supplied repository index and GitHub sources; do not inspect the full repository. Ground each idea in the project's actual behavior and code evidence.

1. Expand — What else can it do?
Add capabilities that increase the project's usefulness: new features, integrations, workflows, automation, or supported use cases.
2. Optimize — How can it do more with less?
Improve efficiency without compromising existing behavior: latency, throughput, memory, tokens, infrastructure cost, or algorithmic complexity.
3. Harden — What could go wrong?
Improve correctness, security, reliability, and resilience: edge cases, failure recovery, tests, concurrency bugs, security vulnerabilities, or observability.
4. Simplify — What can we remove or redesign?
Reduce unnecessary complexity and make future development easier: eliminate redundant abstractions, consolidate modules, delete dead code, simplify APIs, or reduce dependencies.

All four ideas must differ in substance from each other and from EVERY previous suggestion for this project, including selected ideas and older suggestions. Compare the underlying problem, proposed change, and outcome, not just titles. Renaming, paraphrasing, narrowing a previous idea, or moving it to another category does not make it new. Never recycle an idea to fill a category. Each description must explain the concrete change, relevant project evidence, and benefit.

Each description must contain at most {{.MaxDescriptionWords}} words. Be concise while retaining the concrete change, project evidence, and benefit.

Return only a JSON array of four objects with category, title, and description. Category values must be exactly "expand", "optimize", "harden", "simplify", each appearing once. If a new useful idea cannot be found for every category, return [] rather than inventing or repeating ideas.
Repository index:
{{.Index}}
GitHub sources:
{{.Sources}}
Previous suggestions:
{{.Previous}}
