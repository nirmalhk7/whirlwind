# Native nono execution

Whirlwind runs providers, ACP terminals and committed checks through **nono 0.81.0** on the local host. Docker, a Linux agent image and an OpenShell gateway are no longer prerequisites. Install the [official nono 0.81.0 release](https://github.com/nolabs-ai/nono/releases/tag/v0.81.0) for your platform, place `nono` on `PATH`, and confirm:

```sh
nono --version
./bin/whirlwind restart
```

The launcher requires `nono 0.81.0` and fails closed if it is missing, incompatible, or cannot apply the sandbox. `WHIRLWIND_NONO_CLI` can select an absolute host-managed binary. Agent-supplied environment exceptions cannot change this selection or nono policy.

Install and authenticate your configured ACP provider on the host. For the bundled Codex profile, the repository specifies:

```sh
npm install --global @agentclientprotocol/codex-acp@2.5.0
```

Use `kind: acp`, `acp_profile: codex` and `command: [codex-acp]` in the existing provider entry. Model and reasoning selections remain in each role's queue. Whirlwind copies only the selected provider's authorized credential files into its private runtime home. Installed Node adapters receive read access to their containing `node_modules` installation so transitive runtime dependencies can load.

## Enforcement

Read-only roles can read their worktree and declared read roots. Coding grants follow `allowed_paths`; declared check output directories and private runtime state remain writable. On macOS, native rules support literal paths and `*`, `?`, and `**` patterns, including creation of new files. Git metadata, project policy and launcher code remain protected from writes. Symlink destinations are checked by the kernel.

Direct outbound network access is blocked. An authenticated local egress broker permits public HTTP/HTTPS on ports 80 and 443, plus explicitly authorized private hosts. Selected MCP calls retain their scoped host broker; its capability and Unix socket stay outside the agent's filesystem grants. TLS clients use the public system CA bundle.

Codex ACP selects `agent-full-access` **inside the enforced nono boundary** to avoid applying a second nested Seatbelt sandbox. Whirlwind's read-only client permissions and nono's filesystem/network rules still apply. An adapter lacking that mode fails with an actionable setup error.

On Linux, the launcher refuses whole-worktree write grants that Landlock cannot subtract protected metadata from, and unsupported glob grants. Use bounded existing literal directories for Linux coding; do not widen permissions to bypass a refusal. Native read-only work and declared check outputs remain supported.

## Recovery and verification

Each managed execution persists its deadline, generation and private `nono.json` lifecycle record before launch. Process-group cleanup must succeed before lifecycle state becomes `Stopped`. Interrupted native runs require clean process-registry evidence before replacement. Check results retain their runtime identity and cleanup proof for review.

Legacy OpenShell code remains for recovery of persisted OpenShell attempts and explicit compatibility tests. It is not selected by default. `WHIRLWIND_EXECUTION_LAUNCHER` is a trusted host override for that compatibility path.

Run the native boundary and repository checks with nono installed:

```sh
node --test scripts/*.test.mjs bridges/claude/*.test.mjs bridges/openshell/*.test.mjs bridges/sandbox/*.test.mjs bridges/nono/*.test.mjs
go test -race ./...
```

The native tests execute actual sandboxed processes: they verify read-only source, protected metadata, scoped new files, credential bootstrap, TLS initialization, direct network denial and ACP terminal write denial. Legacy physical gateway tests remain separate compatibility checks in [OpenShell setup](openshell-setup.md).
