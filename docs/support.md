# Whirlwind support and verification

This page describes the current local workflow. “Implemented” describes a code
path, not a claim that the current release or every installed tool is qualified.

| Area | Status | What to expect |
| --- | --- | --- |
| macOS native execution | Implemented, requires compatible local runtime | Agents, ACP terminals and native checks use macOS and nono 0.81.0. Missing or incompatible runtime fails closed. See [nono setup](nono-setup.md). |
| Linux | Inspection and opt-in offline checks | Inspect saved state with the terminal dashboard or run [offline rootless Docker verification](#verification-on-linux). Sandboxed agents and managed Qlty require macOS. Dedicated test workers can [explicitly run agents as plain processes](nono-setup.md). |
| Project policy | Project-scoped approval implemented | Preview the exact external YAML, then approve its version and hash. Whirlwind stores it outside the checkout. Changed policy requires fresh setup and affected plan approvals. See [project policy approval](go-backend.md#project-policy-approval). |
| Providers | Configured ACP implemented; live qualification is configuration-specific | Configure installed Claude, Codex, Cursor or compatible ACP adapters in the authoritative YAML. Bundled Codex uses the Codex ACP profile, not native app-server transport. A catalog response is not proof of login, inference, tools, resume, fallback, or effective model/thinking. See [configured acceptance](acp-configured-acceptance.md). |
| Project quality setup | Native managed defaults and qualification wired | Native onboarding defaults to Qlty with Gitleaks only. The supervisor prepares managed configuration outside the source checkout, probes the catalog and installed scanner, then records the runtime identity on successful qualification. Human-owned configuration takes precedence. Each runtime and configuration requires its own evidence. See [project quality setup](go-backend.md#project-quality-setup). |
| Database | Current layout implemented; migration waived | Obsolete development state is discarded, not converted. Current state survives reopen. See [SQL schema and lifecycle](persistence.md#project-idea-and-plan-tables). |
| Ideas | Content validation implemented, uniqueness deferred | Ideation uses bounded history for Expand, Optimize, Harden, and Simplify. New proposals and draft edits record `not_checked` / `deferred`. A historical fingerprint match can still yield a draft. Semantic comparison, an ACP novelty judge, embeddings and sqlite-vec remain deferred. |
| Plans | Separate approvals and frozen versions implemented | One plan per idea. Planner completion means a draft is ready for separate approval, not local delivery. A changed save must wait for active approved execution to finish or be explicitly paused, and then requires separate approval. An unchanged save leaves execution running. Only successful local finalization makes the plan complete. |
| Delivery | Local workflow; no agent remote publication | The dashboard displays the verified local commit and evidence; the operator decides whether to publish. Old browser, remote-publication, OpenShell, and native app-server results do not certify the current workflow. |

Run the [repository regression checks](../README.md#verify) on the revision being
evaluated. Fixtures can test persistence, report contracts and fail-closed
behavior without qualifying macOS enforcement or real provider accounts. A
skipped, incomplete or historical check does not establish current acceptance.
See the [source snapshot and later decisions](design/whirlwind-plan-source.md)
for the distinction between the archived requirements and current scope.

## Verification on Linux

- **Standalone.** `whirlwind-verify` accepts a private manifest on stdin. It
  runs the declared commands against the frozen Git tree using a digest-pinned
  local image in rootless Docker and records the result in the job report.
- **Managed setup and candidate checks.** Approve an external project policy
  with `offline_docker_image` and mandatory check commands. Setup qualifies
  the image and approved checks against the current project commit. Later
  candidate checks bind the resulting image identity to the approved policy,
  plan, commands, and exact Git tree.
- **Managed review and delivery.** Review accepts qualified managed offline
  evidence for the approved candidate and rechecks its retained output. Live
  Reviewer and Finalizer agents still require native macOS/nono. Local
  delivery also requires qualified Qlty evidence, which the offline runner
  cannot produce.

See the [offline Docker verifier runbook](offline-docker-verifier.md) for
manifest fields, image prerequisites, and reproduction commands.
