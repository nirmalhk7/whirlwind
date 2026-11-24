# Whirlwind support and verification

This page describes the current local workflow. “Implemented” describes a code
path, not a claim that the current release or every installed tool is qualified.

| Area | Status | What to expect |
| --- | --- | --- |
| macOS native execution | Implemented; needs a compatible local runtime | Agent execution, ACP terminals and committed checks require macOS and nono 0.81.0. Missing or incompatible runtime fails closed. See [nono setup](nono-setup.md). |
| Linux | Inspection only; native execution unsupported | Inspect saved state with the terminal dashboard, including read-only database mode when disconnected. The launcher rejects agent/check execution before invoking nono. Installing nono or changing write grants does not enable it. |
| Providers | Configured ACP implemented; live qualification is configuration-specific | Configure installed Claude, Codex, Cursor or compatible ACP adapters in the authoritative YAML. Bundled Codex uses the Codex ACP profile, not native app-server transport. A catalog response is not proof of login, inference, tools, resume, fallback, or effective model/thinking. See [configured acceptance](acp-configured-acceptance.md). |
| Project quality setup | Managed defaults and qualification wired; native compatibility must be measured | Default onboarding uses Qlty with Gitleaks only. The supervisor prepares managed configuration outside the source checkout, probes catalog and installed scanner, and records actual runtime identity when qualification succeeds. Existing human configuration is not replaced by the defaults. A live result for one runtime does not qualify another. See [project quality setup](go-backend.md#project-quality-setup). |
| Database | Current layout implemented; migration waived | Obsolete development state is discarded, not converted. Current state survives reopen. See [SQL schema and lifecycle](persistence.md#project-idea-and-plan-tables). |
| Ideas | Content validation implemented; uniqueness deferred | Keep bounded history and the four types: Expand, Optimize, Harden, Simplify. Repeated drafts are allowed. No temporary ACP novelty judge, historical fingerprint rejection, embedding runtime or sqlite-vec is required. |
| Plans | Separate approvals and frozen versions implemented | One plan per idea. Planner completion means a draft is ready for separate approval, not local delivery. A changed save must wait for active approved execution to finish or be explicitly paused, and then requires separate approval. An unchanged save leaves execution running. Only successful local finalization makes the plan complete. |
| Delivery | Local workflow; no agent remote publication | The dashboard displays the verified local commit and evidence; the operator decides whether to publish. Old browser, remote-publication, OpenShell, and native app-server results do not certify the current workflow. |

Run the [repository regression checks](../README.md#verify) on the revision being
evaluated. Fixtures can test persistence, report contracts and fail-closed
behavior without qualifying macOS enforcement or real provider accounts. A
skipped, incomplete or historical check does not establish current acceptance.
See the [source snapshot and later decisions](design/whirlwind-plan-source.md)
for the distinction between the archived requirements and current scope.
