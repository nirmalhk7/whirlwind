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
