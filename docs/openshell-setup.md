# NVIDIA OpenShell setup for Whirlwind

Whirlwind's bridge targets the NVIDIA OpenShell v0.1.7 local CLI and structured output. Agent execution fails closed when the CLI, gateway, image, or policy is unavailable. Opening the terminal dashboard does not require a running sandbox.

## Install and select a local gateway

Follow NVIDIA's [installation guide](https://docs.nvidia.com/openshell/latest/about/installation), then verify the installed version and selected gateway:

```sh
openshell --version
openshell status
openshell gateway list --output json
```

Whirlwind requires a local Docker or Podman gateway. Its gateway entry must report `is_remote: false` with a loopback endpoint (`127.0.0.1`, `::1`, or `localhost`), and status must report `connected`. A remote gateway cannot use the supervisor's loopback broker or these host bind mounts. A different CLI release requires compatibility verification; do not assume command flags or JSON envelopes stay identical.

Select the gateway, endpoint, and workspace in the supervisor's environment using `OPENSHELL_GATEWAY`, `OPENSHELL_GATEWAY_ENDPOINT`, and `OPENSHELL_WORKSPACE`. Execution records pin this authority so recovery cannot silently switch to another gateway or workspace.

## Enable the narrowly generated bind mounts

The launcher supplies `--driver-config-json` with bind mounts for the exact worktree, explicit read roots, individual selected credential files, and explicit writable roots. It does not mount the whole home directory or `.ssh`.

OpenShell disables caller-supplied driver configuration and raw bind mounts by default. For a local Docker gateway, the operator must configure the selected driver in its gateway TOML file. The v0.1.7 schema uses `compute_driver` and schema version 2:

```toml
[openshell]
version = 2

[openshell.gateway]
compute_driver = "docker"

[openshell.drivers.docker]
allow_driver_config = true
enable_bind_mounts = true

[openshell.drivers.docker.resource_admission]
enabled = false
```

For Podman, use `compute_driver = "podman"` and put the same `allow_driver_config`, `enable_bind_mounts`, and `resource_admission.enabled = false` settings under `[openshell.drivers.podman]`.

OpenShell documents bind mounts as an operator security decision because they expose gateway-host paths and can weaken isolation. Disabling resource admission is required for caller-provided raw bind mounts in the documented Docker/Podman setup. The gateway must be restarted after changing its configuration. See NVIDIA's [gateway configuration](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/configuration) and [sandbox driver mount requirements](https://docs.nvidia.com/openshell/latest/reference/sandbox-compute-drivers).

The installed package's config path depends on how OpenShell is installed. The [configuration reference](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/configuration) lists package-managed paths and precedence. Do not overwrite an existing gateway file; edit only the selected driver's settings after reviewing its current contents.

## Supply a Linux workload image

Set `WHIRLWIND_OPENSHELL_IMAGE` in the Whirlwind supervisor environment to an image already visible to the selected local driver. OpenShell's `--from` accepts an image reference; it does not build a Dockerfile or install tools into that image.

The image must provide:

- Linux userspace and `/bin/sh`.
- The selected ACP command exactly as configured: `codex-acp`, `claude-agent-acp`, or `agent acp`.
- Linux `node` for the guest MCP shim, plus any coding, test, and review tools required for the selected role.
- A non-root workload identity with filesystem permissions compatible with the exact read-write host mounts.

For a read-write mount, the workload's effective UID/GID must have write access on the gateway host. OpenShell Docker/Podman policies can choose `process.run_as_user` and `process.run_as_group`; these must agree with host mount ownership and the image's installed user. macOS/OrbStack path translation and ownership must be demonstrated with a disposable test worktree before enabling production runs. Whirlwind does not assume that a Mac path or host executable is usable inside the Linux guest.

The owner can build a disposable fake-ACP image to verify the boundary without model credentials or quota. The production workload image remains operator-supplied.

## Credentials and egress

Whirlwind passes only the selected profile's bounded auth file and environment names that the project execution policy explicitly allows:

| ACP profile | Host auth source | Sandbox destination |
| --- | --- | --- |
| Codex | `~/.codex/auth.json` | `$CODEX_HOME/auth.json` (`$HOME/codex/auth.json`) |
| Claude | `~/.claude/.credentials.json` | `$CLAUDE_CONFIG_DIR/.credentials.json` (`$HOME/claude/.credentials.json`) |
| Cursor | `~/.cursor/cli-config.json` | `$HOME/.cursor/cli-config.json` (`$HOME/.cursor/cli-config.json`) |

Each execution creates a private writable `HOME=/tmp/ww-<executionID>/home`, `CODEX_HOME=$HOME/codex`, `CLAUDE_CONFIG_DIR=$HOME/claude`, and `TMPDIR=/tmp/ww-<executionID>/tmp`. Selected auth files are staged individually read-only under a separate `/tmp/ww-native-<executionID>/` root, then copied with mode `0600` into the private home. Agent cache/session writes remain possible without exposing unrelated host home state. Host credential permissions are not changed.

Each source must be a regular non-symlink file. Missing credentials produce an actionable setup error. Other home state, SSH credentials, plugin caches, and MCP integration configuration are not included by this auth-only grant.

Every workload process is confined by OpenShell policy to the loopback-bound local egress broker through the exact `host.openshell.internal:<ephemeral-port>` TCP endpoint. That endpoint uses OpenShell's narrowly scoped `tls: skip` passthrough so the private broker receives its authenticated proxy protocol without OpenShell rewriting its authority. Workload HTTP clients receive `HTTP_PROXY` and `HTTPS_PROXY`. The broker resolves names itself, pins the selected IP, permits public TCP ports 80 and 443, and denies port 22 and every other port. A private address is eligible only for an exact hostname explicitly present in the profile's allowed-domain list; loopback, link-local, unspecified, metadata, multicast, documentation, and transition ranges remain denied. A client that ignores proxy environment settings stays network-blocked by the OpenShell policy.

Selected integration connections use a separate bounded MCP handler. Their upstream credentials remain with the supervisor; the guest receives only the declared broker capability and authorized read tools. Integration and verification-check handlers are separate, and ambiguous combined selection is rejected. Prompt annotations select existing authorization; they never create it.

See NVIDIA's [policy schema](https://docs.nvidia.com/openshell/latest/how-it-works/policies/schema), [network rules](https://docs.nvidia.com/openshell/latest/how-it-works/policies/network-rules), and [Docker driver notes](https://github.com/NVIDIA/OpenShell/blob/main/crates/openshell-driver-docker/README.md) for the gateway-host `host.openshell.internal` behavior.

## Physical acceptance

Unit fixtures validate policy generation and protocol behavior. They cannot prove Docker mounts, Landlock enforcement, or actual network boundaries. Use a disposable local gateway, workspace, non-root Linux fake-ACP image, and repository to run the opt-in tests without model accounts:

```sh
WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE=1 \
WHIRLWIND_RUN_LIVE_OPENSHELL=1 \
WHIRLWIND_OPENSHELL_CLI=/absolute/path/openshell \
WHIRLWIND_OPENSHELL_IMAGE=your-disposable-fake-acp-image \
OPENSHELL_GATEWAY=your-test-gateway \
OPENSHELL_GATEWAY_ENDPOINT=http://127.0.0.1:your-port \
OPENSHELL_WORKSPACE=your-test-workspace \
go test -p 1 ./internal/execution ./internal/provider ./internal/verification ./internal/workflow \
  -run '^(TestLiveOpenShell(Acceptance|ProcessAdapterACPHandshake)|TestACPTerminalCannotWriteOutsideSandbox|TestCheckRunnerUsesSandboxProfile|TestCheckOutputGrantKeepsSourceReadOnly|TestEngineOwnedCheckOpenShellAcceptance|TestOwnedCheckLiveOpenShellKeepsSourceReadOnlyAndGrantsOnlyDeclaredOutput)$' -count=1 -v
```

Use the matching private `XDG_CONFIG_HOME` and `XDG_STATE_HOME` when the test gateway is configured outside the default OpenShell directories. The fake image must implement the credential-free ACP protocol expected by the test; this is not a production agent image.

The terminal filesystem-boundary regression requires live configuration and fails when it is absent. Select it in the physical run above after running the controlled-fixture suite in the README. Keep real gateway environment variables local to physical test commands so they do not override the gateways selected by unit fixtures.

Acceptance covers role-specific read/write mounts, denied outside writes, public HTTP/HTTPS and DNS, direct network bypass denial, port 22 and unapproved/private targets, isolated synthetic auth and writable agent homes, strict timeout cleanup, and ACP model/thinking confirmation. ACP completion must leave no execution-owned sandbox behind. Run these checks after final edits; skipped tests or earlier results do not prove the current bridge.

Remove only the disposable resources whose execution identity and workspace match the test. Production credentials and unrelated gateway resources are outside this acceptance workflow.
