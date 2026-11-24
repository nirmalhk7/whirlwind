"""Drive the shipped dashboard through two PTYs with seeded supervisor state."""

import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import time

from dashboard_pty_support import PTYSession

ENTER = b"\r"
ESCAPE = b"\x1b"
DOWN = b"\x1bOB"
F6 = b"\x1b[17~"
PAGE_DOWN = b"\x1b[6~"
TIMEOUT = 12


def checked_wait(session, predicate, description):
    deadline = time.monotonic() + TIMEOUT
    while not predicate():
        if time.monotonic() >= deadline or not session.read_available(0.05):
            raise AssertionError(f"waiting for {description}:\n{terminal_text(session.output)}")


TERMINAL_TOKEN = re.compile(
    r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|"
    r"\x1b[()][A-Za-z0-9]|\x1b[@-_]|[\r\n\b]|[^\x00-\x1f\x7f]+"
)


def terminal_text(output):
    """Reconstruct visible ASCII text from tcell's positioned redraws."""
    rows, columns = 32, 112
    screen = [[" "] * columns for _ in range(rows)]
    row, col = 0, 0
    for token in TERMINAL_TOKEN.findall(output.decode("ascii", errors="replace")):
        if token.startswith("\x1b["):
            params, action = token[2:-1], token[-1]
            if action in "Hf":
                coords = params.split(";")
                row = int(coords[0] or "1") - 1
                col = (int(coords[1] or "1") if len(coords) > 1 else 1) - 1
            elif token in ("\x1b[2J", "\x1b[?1049h", "\x1b[?1049l"):
                screen = [[" "] * columns for _ in range(rows)]
            elif action == "K" and 0 <= row < rows:
                screen[row][col:] = [" "] * (columns - col)
        elif token.startswith("\x1b"):
            continue
        elif token == "\r":
            col = 0
        elif token == "\n":
            row += 1
        elif token == "\b":
            col = max(0, col - 1)
        else:
            for char in token:
                if 0 <= row < rows and 0 <= col < columns:
                    screen[row][col] = char
                col += 1
    return "\n".join("".join(row) for row in screen)


def see(session, marker, start=0, *, raw=False):
    def rendered():
        if len(session.output) <= start:
            return False
        if raw:
            return marker in session.output[start:]
        return marker.decode("ascii") in terminal_text(session.output)
    checked_wait(session, rendered, marker)
    return len(session.output)


def send(session, keys):
    start = len(session.output)
    if os.write(session.master, keys) != len(keys):
        raise AssertionError(f"incomplete PTY key write: {keys!r}")
    return start


def step(session, keys, marker):
    see(session, marker, send(session, keys))


def requests(data):
    path = data / "requests.jsonl"
    if not path.exists():
        return []
    raw = path.read_bytes()
    # A concurrent append may still be completing the last record.
    return [json.loads(line) for line in raw.split(b"\n")[:-1]]


def decision(session, data, operation, count, keys, *, error=""):
    send(session, keys)

    def latest():
        hits = [entry for entry in requests(data) if entry["Request"]["operation"] == operation]
        return hits[count - 1] if len(hits) >= count else None

    checked_wait(session, latest, f"{operation} #{count}")
    observed = latest()["Error"]
    if observed != error:
        raise AssertionError(f"{operation}: expected error {error!r}, got {observed!r}")


def close_modal(session):
    # Escape is ambiguous with the start of an xterm control sequence. Wait
    # for the dashboard to redraw before sending input for the restored pane.
    send(session, ESCAPE)

    def restored_main():
        header = terminal_text(session.output).splitlines()[0]
        return "Projects" in header[:26] and "Inbox" in header[26:]

    checked_wait(session, restored_main, "restored dashboard panel headers")


def quit_session(session):
    if code := session.quit_and_wait():
        raise AssertionError(f"dashboard exited {code}: {bytes(session.output)[-7000:]!r}")


def idea(session):
    step(session, b"v", b"Saved idea and provenance")


def select_plan(session, offset, title, *, keep_open=False):
    step(session, b"t", b"Enter to inspect")
    step(session, b"\x1bOH" + DOWN * offset + ENTER, title)
    if not keep_open:
        close_modal(session)


def initial_session(session, data):
    see(session, b"Terminal Fixture")
    (data / "block-snapshot").touch()
    try:
        checked_wait(session, lambda: (data / "block-snapshot-entered").exists(), "blocked background snapshot")
        step(session, b"?", b"Keyboard instructions")
        close_modal(session)
    finally:
        (data / "block-snapshot-release").touch()
    send(session, b"\t\t\t")
    idea(session)
    decision(session, data, "idea.update", 1, b"STALE" + b"\t" * 4 + ENTER, error="version conflict")
    see(session, b"version conflict")
    see(session, b"Draft advanced elsewhere")
    idea(session)
    see(session, b"Background update preserved")
    decision(session, data, "idea.update", 2, b"APPROVED" + b"\t" * 4 + ENTER)
    see(session, b"decision saved by supervisor")
    see(session, b"sandbox recovery status unverified")
    idea(session)
    decision(session, data, "idea.approve", 1, b"\t" * 5 + ENTER)
    see(session, b"approved")
    step(session, DOWN + b"v", b"Cancel planning")
    decision(session, data, "planner.cancel", 1, ENTER)
    see(session, b"decision saved by supervisor")

    select_plan(session, 1, b"State: plan_approval")
    step(session, b"r", b"F6 plan/controls")
    see(session, b"whirlwind-plan-", send(session, F6 + b"v"), raw=True)
    decision(session, data, "plan.document.save", 1, b":%s/Verify fixture output/Verify revised fixture output/g\r:wq\r")
    see(session, b"decision saved by supervisor")
    see(session, b"sandbox recovery status unverified")
    step(session, b"r", b"Verify revised fixture output")
    decision(session, data, "plan.review", 1, b"\t" + ENTER)

    # Open a saved clarification and leave without submitting a decision.
    select_plan(session, 2, b"Which fixture encoding?")
    step(session, b"c", b"Save answers")
    close_modal(session)
    quit_session(session)


def reopened_session(session, data):
    see(session, b"Terminal Fixture")
    send(session, b"\t\t\t")
    select_plan(session, 2, b"Which fixture encoding?")
    step(session, b"c", b"Save answers")
    decision(session, data, "question.answer", 1, b"UTF-8\t\t" + ENTER)
    select_plan(session, 3, b"delivered-fixture", keep_open=True)
    see(session, b"verification evidence (fixture-verification)")
    see(session, b"fixture-regression")
    for marker in (b"Computed adherence: 90%", b"REQ-OPTIONAL", b"+expected fixture output"):
        if marker.decode() not in terminal_text(session.output):
            step(session, PAGE_DOWN, marker)
    close_modal(session)
    quit_session(session)


def fixture_environment(data):
    home = data / "home"
    home.mkdir(mode=0o700)
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "TMPDIR": str(data),
           "TERM": "xterm-256color", "LANG": "C", "VIMINIT": "set nocompatible",
           "WHIRLWIND_DATA_DIR": str(data)}
    for name in ("GOCACHE", "GOMODCACHE", "GOFLAGS"):
        if name in os.environ:
            env[name] = os.environ[name]
    if shutil.which("vim") is None:
        vi = shutil.which("vi")
        if vi is None or b"Vi IMproved" not in subprocess.check_output([vi, "--version"]):
            raise AssertionError("seeded terminal acceptance requires Vim")
        # Some Linux images expose Vim Tiny only as vi. Keep the real editor
        # and its PTY; skip only its unavailable syntax highlighting command.
        shim = data / "vim"
        shim.write_text('#!/bin/sh\nexec "$WHIRLWIND_FIXTURE_VI" -n -c "setlocal filetype=markdown" -- "$7"\n')
        shim.chmod(0o700)
        env["WHIRLWIND_FIXTURE_VI"] = vi
        env["PATH"] = f"{data}{os.pathsep}{env['PATH']}"
    return env


def main():
    data = Path(sys.argv[1])
    env = fixture_environment(data)
    for drive in (initial_session, reopened_session):
        session = PTYSession(env)
        try:
            drive(session, data)
        finally:
            session.close()
    print("PASS: the shipped dashboard sent versioned mutations over the private Unix socket, rejected a stale decision, reopened pending clarification, and inspected local delivery.")


if __name__ == "__main__":
    main()
