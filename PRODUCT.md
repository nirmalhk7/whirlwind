# Whirlwind

<!-- impeccable:product-schema 1 -->

## Platform

Go terminal application

## Users

A single local operator using Whirlwind as a daily working control center. Confirmed
by the user on October 12, 2026.

## Product Purpose

Turn approved plans into durable, inspectable implementation, verification, review,
and local delivery workflows. The operator selects directions, approves plans,
answers questions, resolves permissions, and decides whether to publish.

## Positioning

Whirlwind coordinates multiple supported agents behind one Go workflow engine,
with SQLite state, isolated Git worktrees, deterministic checks, structured
review findings, and recovery across interrupted attempts.

## Operating Context

Local Go/tview dashboard opened with `./bin/whirlwind dashboard`, backed by a
durable local supervisor and private Unix command/event interface. No browser
frontend or HTTP UI server runs. Existing SQLite records, paused plans, questions,
permissions, worktrees, and recovery counters survive closing the terminal.

## Capabilities and Constraints

Five roles: Ideation, Planner, Coding, Reviewer, and Finalizer. Independent YAML
queues select ACP providers, models, and supported thinking settings. Bundled
Claude, Codex, and Cursor profiles require compatible installed, authorized
runtimes. Every agent runs inside NVIDIA OpenShell in its own Git worktree.

Direction selection and plan approval are separate human decisions. Approval
freezes the specification and requirement IDs. Coding and review repeat until the
unrounded requirement score is strictly greater than 80% and all mandatory gates
pass for the same candidate: plan checks, configured tests/build, and no new Qlty
findings. Failed and unverified outcomes earn no credit.

Finalization preserves the exact accepted snapshot locally and displays its
branch, commit, diff, review, unmet requirements, and verification evidence.
Agents cannot push, create PRs, or merge. Pending questions and permissions pause
the affected plan; changed specifications require renewed human approval.

Each attempt has a fixed 300-second deadline. Persisted recovery allows three
restarts after the initial launch, then the same role's next configured choice.
Old writers and interrupted effects must be reconciled before replacement work.
Missing credentials, unknown ownership, and unavailable enforcement fail closed.

## Brand Commitments

Whirlwind: autonomous software engineering, in continuous motion. Wordmark
`whirlwind_`; signature `ENGINEERING IN MOTION`; primary message
`Ideas in. Verified software out.` Agent-agnostic, ACP-native, durable execution,
and evidence-driven. Velocity, control, and correctness guide the identity.
Midnight background, cloud white text, ion teal actions, velocity blue information,
energy violet evidence, and signal amber focus. Preserve this daily operating
control center and existing navigation; branding must not obscure decisions.

## Evidence on Hand

The unchanged Whirlwind Plan, PLAN.md, API.md, the current Go implementation,
and local automated workflow fixtures. No comparative performance benchmarks
or universal capability claims are available; do not invent them.

## Product Principles

- Make the next operator action obvious.
- Expose truthful state and durable progress.
- Express confidence through control and execution discipline.
- Keep errors, unavailable providers, and human decisions clearly visible.
