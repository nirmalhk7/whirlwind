# Whirlwind

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

A single local operator using Whirlwind as a daily working control center. Confirmed
by the user on October 12, 2026.

## Product Purpose

Turn selected project directions and eligible GitHub issues into durable,
inspectable implementation and review workflows. The operator chooses work,
answers questions, resolves permissions, and decides whether a PR should merge.

## Positioning

Whirlwind coordinates multiple supported agents behind one Go workflow engine,
with SQLite state, isolated Git worktrees, deterministic checks, structured
review findings, and recovery across interrupted attempts.

## Operating Context

Local browser application backed by a loopback Go service. Projects require
trusted local GitHub repositories, authenticated gh, and committed policy.
Daily ideation, active tasks, review gates, and Inbox decisions are recurring
operator work. Inbox is the default screen. Agents configures role routing;
Providers manages connected model sources. Tasks is absent from the menu, while
the active task view at /tasks remains accessible from Inbox. Workspace navigation
is removed. Projects includes a local folder selector that fills the repository
path without uploading files.

## Capabilities and Constraints

Four agent roles: ideation, planning, coding, review. Codex, Cursor, Claude,
and configurable ACP providers. Antigravity remains visibly unavailable.
Publication requires passing checks, a review score of at least 9/10, no
blocking findings, and explicit approval for critical-risk changes. Whirlwind does
not merge automatically. Provider availability is not proof of authentication;
unknown quota remains unknown.

Agents at /agents configures the fixed ideate, plan, code, and review roles. Each
role has a primary provider and explicit model, followed by ordered provider/model
fallback pairs. Load models requests the source's real model metadata; Test model
sends Hello World to the selected pair. Saving uses versioned configuration.

Configured chains preserve the operator's exact order, skip ineligible or exhausted
sources, and advance on quota or authentication failure. Ordinary failures pause
the workflow; exhausting every source stops it. Quotas are shared across models
from the same source. Sessions retain their selected model, and fallback preserves
worktree and context handoff. Unconfigured roles retain legacy project routing.

## Brand Commitments

Whirlwind: autonomous software engineering, in continuous motion. Wordmark
`whirlwind_`; signature `ENGINEERING IN MOTION`; primary message
`Ideas in. Verified software out.` Agent-agnostic, ACP-native, durable execution,
and evidence-driven. Velocity, control, and correctness guide the identity.
Midnight background, cloud white text, ion teal actions, velocity blue information,
energy violet evidence, and signal amber focus. Preserve this daily operating
control center and existing navigation; branding must not obscure decisions.

## Evidence on Hand

PLAN.md, API.md, docs/go-backend.md, the current Go and Next.js implementation,
and local automated workflow fixtures. No comparative performance benchmarks
or universal capability claims are available; do not invent them.

## Product Principles

- Make the next operator action obvious.
- Expose truthful state and durable progress.
- Express confidence through control and execution discipline.
- Keep errors, unavailable providers, and human decisions clearly visible.
