#!/usr/bin/env python3
"""Real-PTY acceptance for the canonical dashboard launcher and durable supervisor."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from dashboard_pty_support import RENDER_MARKER, dashboard_pty


REPO = Path(__file__).resolve().parent.parent
ENTRY = REPO / "bin" / "whirlwind"


def run_cli(env, command, timeout=300, expected_code=0):
    result = subprocess.run(
        [str(ENTRY), command],
        cwd=REPO,
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )
    if result.returncode != expected_code:
        log = Path(env["WHIRLWIND_DATA_DIR"]) / "whirlwind-supervisor.log"
        diagnostics = (
            log.read_text(encoding="utf-8", errors="replace").splitlines()[-12:]
            if log.is_file()
            else []
        )
        raise AssertionError(
            f"./bin/whirlwind {command} returned {result.returncode}, expected {expected_code}: "
            f"{result.stdout}{result.stderr}" + "\n".join(diagnostics)
        )
    return result.stdout + result.stderr


def main(project=None):
    scratch = Path(tempfile.mkdtemp(prefix="whirlwind-dashboard-pty-"))
    scratch.chmod(0o700)
    data = scratch / "data"
    data.mkdir(mode=0o700)
    home = scratch / "home"
    home.mkdir(mode=0o700)
    runtime = scratch / "runtime"
    runtime.mkdir(mode=0o700)
    settings = scratch / "settings.yaml"
    project_marker = b"No projects"
    if project is not None:
        settings.write_text(
            json.dumps(
                {
                    "version": 1,
                    "projects": [str(project)],
                    "providers": [],
                    "agents": {},
                    "workers": {"stopped": True, "ideation": {"on_registration": False}},
                }
            ),
            encoding="utf-8",
        )
        project_marker = project.name.encode("utf-8")
    env = {
        "PATH": os.environ["PATH"],
        "HOME": str(home),
        "TMPDIR": str(runtime),
        "GOMODCACHE": os.environ.get("GOMODCACHE", str(Path.home() / "go/pkg/mod")),
        "GOCACHE": os.environ.get("GOCACHE", str(runtime / "go-build-cache")),
        "TERM": "xterm-256color",
        "WHIRLWIND_ROOT": str(REPO),
        "WHIRLWIND_DATA_DIR": str(data),
        "WHIRLWIND_SETTINGS_FILE": str(settings),
        "CODEX_HOME": str(home / "codex"),
        "CLAUDE_CONFIG_DIR": str(home / "claude"),
        "XDG_CONFIG_HOME": str(home / "xdg-config"),
        "XDG_STATE_HOME": str(home / "xdg-state"),
        "XDG_CACHE_HOME": str(home / "xdg-cache"),
    }
    state_path = data / "control" / "supervisor.json"
    started = False
    start_attempted = False
    preserve_scratch = False
    try:
        start_attempted = True
        run_cli(env, "start")
        started = True
        before = json.loads(state_path.read_text(encoding="utf-8"))
        first_output = dashboard_pty(env, project_marker=project_marker)
        if run_cli(env, "status").find("supervisor: running") < 0:
            raise AssertionError("supervisor did not survive the first dashboard exit")
        after_first = json.loads(state_path.read_text(encoding="utf-8"))
        if before != after_first:
            raise AssertionError(
                "first dashboard exit changed supervisor identity or durable state"
            )
        second_output = dashboard_pty(
            env,
            rows=24,
            columns=80,
            additional_markers=(b"? all keys | Tab/Shift-Tab panels",),
            project_marker=project_marker,
        )
        if run_cli(env, "status").find("supervisor: running") < 0:
            raise AssertionError("supervisor did not survive reopening the dashboard")
        after_second = json.loads(state_path.read_text(encoding="utf-8"))
        if before != after_second:
            raise AssertionError("reopening the dashboard reset or replaced supervisor state")
        if RENDER_MARKER not in first_output or RENDER_MARKER not in second_output:
            raise AssertionError("one of the PTY sessions did not render the dashboard")
        if project is not None:
            run_cli(env, "restart")
            restarted = json.loads(state_path.read_text(encoding="utf-8"))
            if restarted == before:
                raise AssertionError("restart retained the same launcher identity")
            dashboard_pty(env, project_marker=project_marker)
            print(f"PASS: {project.name} rendered before and after supervisor restart.", flush=True)
        run_cli(env, "stop")
        started = False
        start_attempted = False
        if state_path.exists() or "supervisor: stopped" not in run_cli(
            env, "status", expected_code=1
        ):
            raise AssertionError("owned scratch supervisor did not stop cleanly")
        print(
            "PASS: exact dashboard command rendered, exited, reopened at 80×24 with compact help, and preserved the same scratch supervisor until owned stop."
        )
    finally:
        if started or start_attempted:
            try:
                run_cli(env, "stop", timeout=30)
                started = False
                start_attempted = False
            except Exception as error:
                preserve_scratch = True
                print(f"scratch supervisor cleanup failed: {error}", file=os.sys.stderr)
                print(
                    f"Scratch state retained for manual owner-scoped cleanup: {scratch}",
                    file=os.sys.stderr,
                )
                raise RuntimeError(
                    f"scratch supervisor cleanup failed; state retained at {scratch}"
                ) from error
        if preserve_scratch:
            print(
                f"Scratch state retained for manual owner-scoped cleanup: {scratch}",
                file=os.sys.stderr,
            )
        else:
            shutil.rmtree(scratch, ignore_errors=True)


if __name__ == "__main__":
    if len(sys.argv) > 2:
        sys.exit("usage: python3 scripts/dashboard-pty-acceptance.py [disposable local checkout]")
    main(Path(sys.argv[1]).resolve() if len(sys.argv) == 2 else None)
