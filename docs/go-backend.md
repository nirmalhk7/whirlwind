# Go backend and project policy

Go owns scheduling, worktrees, configured ACP routing, verification, local
delivery, and SQLite state. The Go/tview dashboard is the human interface; there
is no browser frontend or UI server. The private Unix socket accepts named
local commands. An offline dashboard can read existing SQLite state without
starting a supervisor. Native execution requires macOS and nono 0.81.0. Linux
supports inspection and [opt-in offline verification](support.md#verification-on-linux).
See [supervisor setup](local-supervisor.md) and the [support matrix](support.md).

## Run

```sh
go mod download
./bin/whirlwind start
./bin/whirlwind dashboard
```

Set absolute local Git checkout paths under `projects` in the authoritative
`settings.yaml`. Canonical local ideation can use Git evidence from a checkout
without a remote. If ideation selects GitHub, the source must have readable
issues through `gh`. Read [workflow modes](workflow-modes.md) for approvals,
the execution lifecycle, and local delivery. Use [nono setup](nono-setup.md)
for provider installation, then configure independent Ideation, Planner,
Coding, Reviewer, and Finalizer queues in YAML. One role cannot inherit
another's queue.

## Project policy approval

Register the checkout in `settings.yaml` and start the supervisor. Prepare a
YAML policy outside the checkout using the [policy format](#project-policy).

```sh
./bin/whirlwind policy preview /path/to/checkout /path/to/policy.yaml
./bin/whirlwind policy approve /path/to/checkout /path/to/policy.yaml VERSION HASH
```

Review the printed policy and use the version and SHA256 from that preview.
The command accepts a registered project ID in place of the checkout path.
Approval stores the exact reviewed policy in Whirlwind's SQLite state outside
the checkout. A new policy hash queues fresh setup and requires reapproval
for plans bound to an older hash. Preview again after editing the input YAML
or if the supervisor reports a version conflict. Edits take effect after a
fresh approval.

Existing projects can continue using a committed `.whirlwind.yml` or
`.whirlwind.yaml`. Changes to a committed policy require confirmation.
Choose the [project-scoped external policy](offline-docker-verifier.md#managed-setup-and-candidate-checks)
for managed offline Docker checks.

## Project quality setup

For native managed defaults, setup probes the installed Qlty version and Gitleaks
catalog entry, stages the managed configuration in an external per-project
quality directory, and qualifies the installed scanner before recording
readiness. Gitleaks is the **only** default onboarding analyzer. The defaults
do not override human-owned analyzer configuration or put generated scanner
state in the checkout. Required unsupported or unavailable tools block
readiness; they are not silently skipped. Fixture coverage of catalog,
preparation and qualification wiring is not proof of native compatibility.
Use real macOS/nono/Qlty evidence for the exact runtime and configuration.
Linux offline verification runs approved mandatory commands without managed
Qlty preparation or source-changing autofix.

The approved candidate command is
`qlty check --upstream HEAD --fix --no-progress --sarif` in isolated staging,
with the approved base exposed as local `HEAD`. The no-new-findings gate stays
mandatory at every severity. Successful execution or a single fixture finding
does not establish complete finding coverage across plugins or scan scopes.

## Project policy

Example for a Go project:

```yaml
checks:
  - name: formatting
    command: [sh, -c, 'test -z "$(gofmt -l cmd internal)"']
  - name: build
    command: [go, build, ./...]
  - name: tests
    command: [go, test, ./...]
  - name: static-analysis
    command: [go, vet, ./...]
allowed_paths: ['cmd/**', 'internal/**', 'go.mod', 'go.sum', 'docs/**']
commands:
  - [go, test]
  - [go, build]
  - [go, vet]
risk_rules:
  - level: high
    paths: ['internal/auth/**', 'internal/store/**']
    checks:
      - name: race
        command: [go, test, -race, ./...]
  - level: critical
    paths: ['internal/payments/**', 'internal/permissions/**']
providers: [codex, claude, cursor]
project_slots: 2
```

Commands are explicit argument arrays; a shell command must name its
interpreter. Declare the project's own checks, file scopes, and build/test
commands. Whirlwind does not add Go checks to non-Go projects. `allowed_paths`
limits Coding writes; Git metadata and Whirlwind policy stay protected.
Ordinary plan approvals, mandatory checks, and critical-change approvals
apply before local finalization. An agent cannot publish a remote branch or PR.

## Evidence and integrations

See [durable execution evidence](persistence.md) for the exact state schema,
artifact limits and retention. The [HTTP API contract](../API.md) describes
retained integration and webhook interfaces; these are not a browser UI or
an alternative to local approval. Find current [regression commands](../README.md#verify)
and [configured ACP qualification](acp-configured-acceptance.md) separately.
The [native app-server record](acp-codex-verification.md) is historical, not a
setup or verification guide for bundled Codex ACP.
