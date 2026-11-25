# What Qlty onboarding verifies

Onboarding qualifies the installed scanner and records its identity. Its
candidate and base are both the registered commit, so the approved HEAD-diff
scan has no repository changes to select. A probe already committed in that
tree is not a positive control for finding detection. An empty report is valid
only when the SARIF evidence is complete and the scanner invocation succeeded;
it is not evidence of a clean full-repository scan.

Candidate verification runs the same exact command against an isolated checkout
with the approved base at HEAD and candidate bytes preserved:

```sh
qlty check --upstream HEAD --fix --no-progress --sarif
```

The portable staging regression `TestQltyStagingSeparatesUnchangedOnboardingFromCandidateDiff`
checks that unchanged onboarding has no tracked or new project files in scope,
and that candidate staging preserves edited files and newly added paths
without altering the original repository. It does not run Qlty.

Native acceptance uses two separate controls on macOS with the approved runtime:

- `TestProjectSetupNativeAutomaticQltyOnboarding` (opt-in with
  `WHIRLWIND_RUN_LIVE_QLTY_ONBOARDING=1`) checks catalog preparation,
  installed scanner identity, successful invocation and readiness for the
  unchanged registered commit.
- `TestPreparedDefaultConfigurationRunsActualGitleaks` requires a known
  finding in a changed file and tests reuse of that qualified scanner identity.
  The positive control is not replaced by the onboarding result.

Neither fixture on its own establishes coverage beyond the approved scope.
The production command, approval and no-new-findings gate are unchanged.
