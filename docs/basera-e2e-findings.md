# Whirlwind end-user test with Basera

> Historical observations from the exact revisions below. They do not qualify
> current HEAD or its managed scanner defaults. See the [support matrix](support.md).

November 23, 2026 · Whirlwind `30de5d9` · Basera `0d95c9d2c113c402d5cfe16476e03a3e860fc6ee` · macOS arm64

Started the shipped `./bin/whirlwind start` launcher and operated `./bin/whirlwind dashboard` in a real terminal. Used a disposable checkout of Basera’s committed `main`, a separate supervisor database, and a copy of Whirlwind settings pointed at that checkout. The run covered a local-only checkout and then the same checkout with Basera’s GitHub origin restored and its push URL disabled. The configured Codex provider was authorized for the live Basera steps. Basera’s original checkout had uncommitted work, which stayed outside the test.

## Observed failures

- **Fresh onboarding fails at quality setup.** Basera’s committed `.whirlwind.yml` lists Qlty and the project checks. The tested revision required a rule registry domain for its former default scanner. Basera’s policy gave no access to that domain, so setup stopped before catalog preparation. Runs `70106be6bfa9b3256893f4e9` and `601a18152a654add0e332413` recorded the same failure before and after adding the GitHub origin.
- **Project health hides failed setup.** Projects showed `basera OK`, Inbox showed `No inbox items`, and Runs showed `Project setup: Preparing quality`. Opening that row with Enter revealed `Status: failed` and the scanner registry error. The saved project status was also `error`. A user can reasonably read the main screen as healthy or still working.
- **A local Git checkout cannot generate ideas without a GitHub origin.** With origin absent, Whirlwind passed Basera’s absolute directory to `gh`. All four directions ended at `0/5` with `expected the "[HOST/]OWNER/REPO" format`. Both initial and manually triggered ideation reproduced this. README presents local Git paths as sufficient for normal onboarding.
- **Adding the origin duplicates the configured project.** Restoring Basera’s origin on the same checkout produced two `basera` rows with the same directory and separate IDs, run histories, and setup attempts. The selection stayed on the obsolete local-only row, so a manual `i` retry repeated its old GitHub error while the new row ran separately.
- **First onboarding starts ideation without a terminal decision.** The supervisor queued ideation alongside setup on both registrations, with the periodic ideation cron empty. On the GitHub-backed row, Codex started while quality setup had already failed. A configured checkout can incur a model run on startup before the user presses `i`.
- **One live Codex attempt died mid-run.** The run log recorded WebSocket `403 Forbidden` and HTTPS fallback, then `provider exited before response: exit status 137`. It also recorded `zsh:1: can't create temp file for here document: operation not permitted` and `zsh:1: command not found: python`. The retry recovered and generated all four ideas. The evidence establishes the sequence, with the cause of exit 137 still unknown.
- **The live planner stalled and paused the approved idea.** The planner recorded five WebSocket `403 Forbidden` responses, then stopped emitting updates. Task Details showed `running` with `heartbeat: expired` until the five-minute deadline. The plan paused with `context deadline exceeded` and `exhausted its provider/model fallback list`. The configured Plan queue had one Codex choice, the restart count was zero, and the expected Markdown file was absent. The idea editor offered `Retry planning`.
- **Retry planning ended without a reviewable plan.** The dashboard accepted the retry. Its first two attempts reported `exit status 128` and `signal: killed`. The next attempt reached a provider completion, but the run log included a shell `operation not permitted` error during a command targeting `plan.md`. The persisted idea returned to `error` and its editor showed `Planner error: statat plan.md: no such file or directory`. Runs showed a completed Codex attempt while the idea stayed blocked and no Markdown was published.
- **Agents omits the advertised model queues and gives limited status context.** The panel listed provider names and connection badges. The configured role choices, model order, and reasoning levels were absent from the panel, even after focusing it and pressing Enter. Codex stayed `unverified` after a completed ideation attempt and later switched to `up` after planning had timed out.
- **Runs uses two incompatible ways to close details.** Enter on a setup or ideation operation opened an `Esc to close` panel. Enter on an individual agent attempt switched to a raw log stream. Esc and Tab stayed in the log, with Esc displayed as `^[`. Ctrl-C returned to the dashboard. The footer lists `Esc close` and labels Ctrl-C as quit.
- **A standard 80×24 terminal hides useful run and agent information.** The shortcut footer took nine rows. Runs showed one complete two-line row and part of another, and Agents cut off provider status after the first entry. Widening the terminal restored room for several more rows.
- **Dispatch briefly reports an unverifiable sandbox identity.** The supervisor log repeatedly recorded `standalone sandbox runtime recovery blocks dispatch: sandbox error: isolation could not be verified: registered standalone runtime identity is incomplete or invalid`. The dashboard continued to show `OK`. Ideation recovered. The duration and affected jobs remain unknown.
- **Setup docs point returning users at the wrong database.** With state overrides unset, `./bin/whirlwind doctor` reported `~/.local/share/whirlwind/parity-v1`. The README promises the default remains `~/.local/share/whirlwind/whirlwind.db`, and the local supervisor guide names the same old database. A returning user following the default launch steps can land in a different state directory.

## Passed in the Basera workflow

- Supervisor started and the dashboard connected. Reopening a dashboard attached to the same supervisor and preserved its run history. The repository’s real-PTY lifecycle check also passed start, render, quit, reopen, and owned stop.
- The GitHub-backed ideation run completed after retry and published four draft ideas. Opened a saved idea in Inbox and approved the Simplify proposal through the terminal editor. Whirlwind saved approval and queued a separate plan.
- Runner stop and resume persisted through the dashboard. Quitting the dashboard left the supervisor running. `./bin/whirlwind stop` stopped the isolated supervisor, and `status` confirmed the stop.

## Coverage limits

- Basera’s live workflow stopped before plan review and approval. Coding, candidate verification, review, and final delivery remain unverified through the Basera dashboard.
- Automatic review blocked the setup retry because of potential access to the former scanner’s rule registry. The user chose to keep that access disabled. Both observed setup failures used Basera’s existing committed policy.
- The test changed only the disposable checkout’s origin configuration and the isolated Whirlwind settings and state. The original checkout and the configured Whirlwind state stayed outside the test. The disposable supervisor is stopped and its evidence is retained under `/tmp/ww-e2e.hX0KiO`.

## Supporting repository checks

- `PYTHONDONTWRITEBYTECODE=1 python3 scripts/dashboard-pty-acceptance.py`, `go build ./cmd/whirlwind ./cmd/whirlwind-dashboard`, and `go vet ./...` passed. Qlty’s structural check exited 0. `qlty check --all` exited 137 with an empty log.
- `node --test scripts/*.test.mjs bridges/claude/*.test.mjs bridges/openshell/*.test.mjs bridges/sandbox/*.test.mjs bridges/nono/*.test.mjs` reported 80 passed and three failures. The failing lifecycle and native before/after hook tests all passed when rerun sequentially.
- The focused Go workflow run passed planner intake, independent verification, and exact local delivery fixtures. Its full-workflow fixture failed on verification marked `invalid` even though the underlying regression test exited 0. Its scoped finalization fixture failed with `local verification setup or recovery requires attention`.
- `go test -race -json -count=1 -timeout=25m ./...` recorded failures in onboarding, project policy requalification, ACP protocol, quality verification, and workflow fixtures. The workflow package was still running at the end of Basera testing. Native-run evidence is in `/tmp/ww-e2e.hX0KiO/go-native.log`.
