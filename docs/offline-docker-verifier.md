# Offline verification on Linux

An operator can set `offline_docker_image` in an approved external project
policy or a private standalone `whirlwind-verify` manifest to run mandatory
commands against a frozen Git tree on Linux. Native nono remains the default.
Managed Review can consume the offline evidence for this policy. Live Review
and Finalizer still require the native read-only role runtime.

## Managed setup and candidate checks

Put `offline_docker_image` and the mandatory check commands in an external
policy document. Use `whirlwind policy preview` and `whirlwind policy approve`
to confirm its project-scoped hash. The supervisor runs the configured checks
against the current project commit and records which local image configuration
actually completed them. Setup must pass before approving an idea plan.

Later checks use the qualified image configuration and bind every result to
the approved policy, plan, command and exact Git tree. Changing the approved
policy clears its qualification. The Linux option supports mandatory checks
with preloaded or vendored dependencies. Managed Qlty setup and live agent
execution continue to require native nono.

Review and the local delivery gate revalidate the completed job, its retained
streams, the qualified image, and the current clean candidate. Local delivery
also requires qualified Qlty evidence. The offline runner does not support
that scanner, so an offline-only report cannot qualify for delivery.

Use the managed rootless Docker socket at `/var/run/docker.sock` with `runc`
and built-in seccomp. Preload an approved Linux workload image and record one
of its exact repository digests. The image must supply the command and its
runtime dependencies. Use vendored dependencies or tools already in the image
for checks that normally download packages. The verifier launches using the
image configuration ID resolved from that local digest. It prevents pulls
during execution and rejects images that declare Docker volumes.

## Run a frozen check

Build the shipped entry point with `go build -o whirlwind-verify ./cmd/whirlwind-verify`.
Set the usual approved plan, configuration and Git identities in a private
job manifest. Use a fresh private evidence directory outside the repository.
The following are the backend-specific fields.

```json
{
  "offline_docker_image": "golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696",
  "commands": [
    {
      "name": "test",
      "command": ["go", "test", "-mod=readonly", "-count=1", "-json", "./..."],
      "timeout_seconds": 80,
      "report_format": "go-test-json"
    }
  ]
}
```

Send the manifest to `./whirlwind-verify < manifest.json`. The verifier
emits JSON progress and a complete event. Read the `passed` field in the
event or retained `report.json` for the check verdict. A failed assertion can
produce a complete report with `passed: false`. The job journal preserves
completed evidence across restart and blocks replays of interrupted commands.
Exit status, cleanup, the image identity and both bounded streams are retained
with the frozen manifest. Changing the committed candidate, changing the job
definition or modifying saved output invalidates the corresponding evidence.

The checkout contains regular files from the approved Git tree, with each
blob checked against Git before launch. The container gets a nonrecursive
read-only bind, a read-only root filesystem, private temporary storage and no
network. It receives neither host credential configuration nor the Docker
socket. The backend rejects submodules, symlinks, source-changing autofix,
output mounts, host environment overrides and managed scanner setup.

## Reproduce on the Cloud runner

The physical test uses a disposable Go repository with standard-library
dependencies and three commits for baseline, red and green. Prepare the
fixture image separately and run the test with its exact local digest.

```sh
WHIRLWIND_TEST_OFFLINE_DOCKER_IMAGE=golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 \
  go test -race ./internal/verification -run '^TestOfflineDocker' -count=1 -v
```

Use the same image with `go test -race -p 1 ./cmd/whirlwind -run
'^TestManagedOfflineVerificationThroughLauncher$' -count=1 -v` to qualify the
shipped supervisor path. Setting `WHIRLWIND_TEST_SEEDED_NONO` to an installed
nono binary also exercises deterministic Reviewer handoffs and delivery
refusals. The Review fixture uses the native profile metadata and returns a
seeded response in process.

## Standalone Linux Qlty qualification

On Linux amd64, an operator can exercise a fixed Qlty/Gitleaks profile against
one exact Git tree with `whirlwind-verify --linux-qlty-probe`. Use a disposable
worker where agent processes cannot access the operator's filesystem, Docker
socket or verification outputs, or run the probe with agents stopped. A private
directory cannot protect evidence from another process with the same Unix
identity.

Use the Linux x86-64 musl binary from
[Qlty 0.668.0](https://github.com/qltysh/qlty/releases/tag/v0.668.0) and the
Linux x64 binary from
[Gitleaks 8.29.1](https://github.com/gitleaks/gitleaks/releases/tag/v8.29.1).
The probe verifies the extracted binary digests before use. Set
`WHIRLWIND_QLTY_PROBE_TOOLS` to the absolute directory containing the two
binaries named `qlty` and `gitleaks`. The Go Alpine image pinned above must
already be present.

With `repo` set to an absolute checkout path and `job_root` set to a new
absolute private directory outside that checkout, run:

```sh
jq -n \
  --arg job_id linux-qlty-probe \
  --arg repository "$repo" \
  --arg root "$job_root" \
  --arg candidate_commit "$(git -C "$repo" rev-parse HEAD)" \
  --arg candidate_tree "$(git -C "$repo" rev-parse 'HEAD^{tree}')" \
  '{job_id:$job_id, repository:$repository, root:$root,
    candidate_commit:$candidate_commit, candidate_tree:$candidate_tree}' |
  ./bin/whirlwind-verify --linux-qlty-probe
```

Read the verdict in `report.json`. A passing probe needs a successful,
findings-free invocation by the pinned scanner, exact tree and image evidence,
and verified container cleanup. The verifier retains the raw streams and
checks their hashes on replay. A failed scan can still write a complete report
with `passed: false`. This standalone report has no managed-project or plan
identity. Managed qualification, review and Finalizer still require their own
approved evidence.

## Read-only ACP fixture worker

The supervisor can run an operator-selected read-only ACP fixture on a
disposable Linux worker. Use a separate local checkout with no Git remotes
and an isolated supervisor data directory. Set these variables in the
supervisor's environment before `./bin/whirlwind start`:

- `WHIRLWIND_SANDBOX=off`
- `WHIRLWIND_OFFLINE_ACP_CHECKOUT` to the absolute disposable checkout
- `WHIRLWIND_OFFLINE_ACP_FIXTURE` to an operator-controlled executable outside
  that checkout and the supervisor data directory
- `WHIRLWIND_OFFLINE_ACP_IMAGE` to the digest of a compatible, locally
  preloaded Docker image

Use the fixture path as the ACP provider's sole command argument. Keep that
provider free of credentials and sandbox grants. The worker mounts the
fixture and its assigned source directory read-only. It cannot use network
access, host callbacks or the Docker socket. This path covers a read-only
fixture such as Ideation. Coding, managed scanner qualification and delivery
require their existing approved paths.

The supervisor saves a private ownership record before asking rootless Docker
to create a worker. It checks the daemon, container identity, confinement
settings and mounts before launch. At exit it checks the runtime's terminal
state, removes that exact container and confirms removal. Restart recovers
only workers that match their records. An ambiguous create, mismatched owner
or unavailable daemon keeps recovery blocked. The fixture transport does not
inherit host process capabilities.
