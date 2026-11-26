# Native nono execution

Whirlwind runs providers, ACP terminals and committed checks through **nono 0.81.0** on macOS. Docker, a Linux agent image and an OpenShell gateway are not prerequisites. On macOS, install the [official nono 0.81.0 release](https://github.com/nolabs-ai/nono/releases/tag/v0.81.0), place `nono` on `PATH`, and confirm:

```sh
nono --version
./bin/whirlwind restart
```

The launcher requires `nono 0.81.0` and fails closed if it is missing, incompatible, or cannot apply the sandbox. `WHIRLWIND_NONO_CLI` can select an absolute host-managed binary. Agent-supplied environment exceptions cannot change this selection or nono policy.

For disposable testing, start a separate supervisor with `WHIRLWIND_NONO=off` and dedicated state and settings paths. The supervisor logs the opt-out and blocks native agent, terminal, and check execution. Inspection and explicitly configured Linux offline Docker verification remain available. Native execution is enabled when the variable is unset or `on`. Set the variable in the supervisor's startup environment and restart the supervisor to apply a change.

Install and authenticate your configured ACP provider on the host. For the bundled Codex profile, the repository specifies:

```sh
npm install --global @agentclientprotocol/codex-acp@2.5.0
```

Use `kind: acp`, `acp_profile: codex` and `command: [codex-acp]` in the existing provider entry. Model and reasoning selections remain in each role's queue. Whirlwind copies only the selected provider's authorized credential files into its private runtime home. Installed Node adapters receive read access to their containing `node_modules` installation so transitive runtime dependencies can load.

## Enforcement

Read-only roles can read their worktree and declared read roots. Coding grants follow `allowed_paths`; declared check output directories and private runtime state remain writable. On macOS, native rules support literal paths and `*`, `?`, and `**` patterns, including creation of new files. Git metadata, project policy and launcher code remain protected from writes. Symlink destinations are checked by the kernel.

Direct outbound network access is blocked. Provider and project `sandbox.allowed_domains` entries give the local nono proxy explicit HTTP/HTTPS host or path grants. Without grants or credential routes, provider egress is blocked. Multiple path grants on a host share one proxy route. Selected MCP calls retain their scoped host broker and its private Unix socket. TLS interception for path rules uses the sandbox-scoped proxy CA. [Verified Codex configuration](acp-configured-acceptance.md#codex-follow-up-2026-11-19)

Codex ACP selects `agent-full-access` **inside the enforced nono boundary** to avoid applying a second nested Seatbelt sandbox. Whirlwind's read-only client permissions and nono's filesystem/network rules still apply. An adapter lacking that mode fails with an actionable setup error.

The native execution builder and nono launcher reject Linux, including read-only agents and checks. Use the [dashboard](local-supervisor.md) to inspect state and a supported macOS host to run the native workflow. Managed and standalone verification on Linux can opt in to [offline Docker](offline-docker-verifier.md) for pinned local images.

## Role-specific lifecycle hooks

Configure agent hook commands in the supervisor's `settings.yaml`. The same role configuration applies to each registered project.

```yaml
agent_hooks:
  code:
    before:
      - name: environment-ready
        command: [go, version]
    after:
      - name: unit-tests
        command: [go, test, ./...]
        timeout_seconds: 120
```

Supported roles are `ideate`, `plan`, `code`, `review`, and `finalizer`. Hooks run in list order, with dependencies named by `depends_on` within the same event run first. To migrate an older `agent_hooks` list from project policy, copy each selected check definition into its role and event in `settings.yaml` and remove the policy's `agent_hooks` section. Use verification commands whose checker code is outside the agent's edit scope. Put scratch files under `TMPDIR`. Commands see a read-only Git snapshot containing tracked and nonignored worktree files, plus approved read roots. The check sandbox blocks network access. Commands can use its isolated runtime directories for caches and reports.

A failed `before` check blocks startup. After provider and terminal cleanup, the sandbox runs `after` checks before returning a successful result. A failed command sends diagnostics back to the same ACP session for up to three repair rounds within the active run budget. If the provider cannot resume, or a check times out or lacks a valid verdict, completion stays blocked. A Finalizer's pending delivery request is released only after its passing round. Together with the independent workflow verification, these hooks complement the existing mid-session red and green TDD checks.

## Recovery and verification

Each managed execution persists its deadline, generation and private `nono.json` lifecycle record before launch. Process-group cleanup must succeed before lifecycle state becomes `Stopped`. Interrupted native runs require clean process-registry evidence before replacement. Check results retain their runtime identity and cleanup proof for review.

Legacy OpenShell code remains for recovery of persisted OpenShell attempts and explicit compatibility tests. It is not selected by default. `WHIRLWIND_EXECUTION_LAUNCHER` is a trusted host override for that compatibility path.

Run the native boundary and repository checks on macOS with nono installed. A skipped native check or Linux launcher refusal is not physical isolation evidence:

```sh
node --test scripts/*.test.mjs bridges/claude/*.test.mjs bridges/openshell/*.test.mjs bridges/sandbox/*.test.mjs bridges/nono/*.test.mjs
go test -race ./...
```

The native tests execute actual sandboxed processes: they verify read-only source, protected metadata, scoped new files, credential bootstrap, TLS initialization, direct network denial and ACP terminal write denial. Legacy physical gateway tests remain separate compatibility checks in [OpenShell setup](openshell-setup.md).

Recorded linked-worktree, UDP/DNS and SSH/22 acceptance, positive controls, tested platform and runtime versions are in [native isolation acceptance](nono-native-acceptance.md).
