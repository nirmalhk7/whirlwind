#!/usr/bin/env python3
"""Run the Basera fixture and Whirlwind's portable workflow acceptance checks."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


REPO = Path(__file__).resolve().parent.parent
BASERA = "https://github.com/nirmalhk7/basera.git"


def run(args, env, *, cwd=REPO, capture=False):
    return subprocess.run(
        args, cwd=cwd, env=env, check=True, text=True, capture_output=capture
    ).stdout


def git(env, repo, *args):
    return run(["git", *args], env, cwd=repo, capture=True).strip()


def check_readme(env, repo, base):
    readme = repo / "README.md"
    initial = readme.read_bytes()
    marker = b"Local documentation smoke check."
    for trailing in (b" ", b""):
        readme.write_bytes(initial + b"\n\n" + marker + trailing + b"\n")
        result = subprocess.run(
            ["git", "diff", "--check", base, "--", "README.md"],
            cwd=repo,
            env=env,
            check=False,
            text=True,
            capture_output=True,
        )
        if trailing:
            if not result.returncode or "trailing whitespace" not in result.stdout:
                raise AssertionError(f"expected README whitespace failure: {result}")
            print(result.stdout.strip(), flush=True)
        elif result.returncode:
            raise AssertionError(f"expected repaired README to pass: {result}")
    if git(env, repo, "diff", "--name-only", base) != "README.md":
        raise AssertionError("the static-check fixture changed paths outside README.md")
    readme.write_bytes(initial)


def main():
    if len(sys.argv) > 2:
        sys.exit("usage: python3 scripts/basera-smoke.py [local Basera checkout]")
    source = str(Path(sys.argv[1]).resolve()) if len(sys.argv) == 2 else BASERA
    with tempfile.TemporaryDirectory(prefix="whirlwind-basera-smoke-") as directory:
        scratch = Path(directory)
        home, checkout = scratch / "home", scratch / "basera"
        home.mkdir(mode=0o700)
        env = {
            "PATH": os.environ["PATH"],
            "HOME": str(home),
            "TMPDIR": str(scratch),
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_TERMINAL_PROMPT": "0",
            "PYTHONDONTWRITEBYTECODE": "1",
            "WHIRLWIND_TEST_SEEDED_PTY": "1",
        }
        for name in ("GOCACHE", "GOMODCACHE"):
            if name in os.environ:
                env[name] = os.environ[name]
        if not shutil.which("go", path=env["PATH"]):
            sys.exit("the Go toolchain from go.mod must be on PATH")
        clone_env = env.copy()
        for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"):
            for key in (name, name.lower()):
                if key in os.environ:
                    clone_env[key] = os.environ[key]
        run(["git", "clone", "--quiet", "--no-local", "--", source, str(checkout)], clone_env)
        git(env, checkout, "remote", "remove", "origin")
        revision = git(env, checkout, "rev-parse", "HEAD")
        print(
            f"Whirlwind {git(env, REPO, 'rev-parse', 'HEAD')} | Basera {revision}",
            flush=True,
        )
        check_readme(env, checkout, revision)

        run([sys.executable, "scripts/dashboard-pty-acceptance.py", str(checkout)], env)
        run(
            [
                "go",
                "test",
                "-race",
                "-count=1",
                "-v",
                "-timeout=5m",
                "./cmd/whirlwind",
                "-run",
                "^Test(SeededHumanWorkflowThroughPTY|RoleQueueHTTPThroughLauncher)$",
            ],
            env,
        )
        if sys.platform.startswith("linux"):
            run(
                [
                    "node",
                    "--test",
                    "--test-name-pattern=Linux launcher restart reaps a registered worker",
                    "scripts/whirlwind-cli.test.mjs",
                ],
                env,
            )
        env["WHIRLWIND_TEST_BASERA_CHECKOUT"] = str(checkout)
        workflow = (
            "BaseraSmokeIdeationUsesCommittedReadme|ParityApprovedIdeaDispatchesRealMarkdownPlanner|"
            "ReviewCorrectionSurvivesReopenedSQLite|ExpiredWorkerLeaseFencesLateResultsForEveryRole|"
            "FinalizationVerificationKeepsRecordedPrerequisiteFailure|FinalizerRejectsFailedMandatoryStaticCheck|"
            "LoadAcceptedReviewReportRequiresCompleteEvidence|AcceptedCandidateCompletesAsExactLocalDelivery"
        )
        run(
            [
                "go",
                "test",
                "-race",
                "-count=1",
                "-v",
                "-timeout=8m",
                "./internal/workflow",
                "-run",
                f"^Test({workflow})$",
            ],
            env,
        )
        if (
            git(env, checkout, "status", "--porcelain")
            or git(env, checkout, "remote")
            or git(env, checkout, "rev-parse", "HEAD") != revision
        ):
            raise AssertionError("the Basera fixture changed its input checkout")
        print(
            "PASS: local fixtures and Basera static check. Live qualification is separate.",
            flush=True,
        )


if __name__ == "__main__":
    main()
