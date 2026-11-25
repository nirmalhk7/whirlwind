"""Exercise saved tail output, close keys, and tty state restoration."""

import errno
import os
import pty
import select
import signal
import subprocess
import sys
import termios
import time


def run_session(close_key):
    pid, terminal = pty.fork()
    if pid == 0:
        before = termios.tcgetattr(0)
        child = subprocess.run(
            [sys.argv[1], "-test.run=^TestTerminalTailKeysInRealPTY$"],
            check=False,
        )
        if termios.tcgetattr(0) != before:
            os.write(1, b"\nTTY_STATE_NOT_RESTORED\n")
            os._exit(1)
        os._exit(child.returncode)

    output = bytearray()
    exit_status = None

    def read_until(marker, seconds):
        deadline = time.monotonic() + seconds
        while marker not in output and time.monotonic() < deadline:
            poll = max(0, deadline - time.monotonic())
            ready, _, _ = select.select([terminal], [], [], poll)
            if not ready:
                return False
            try:
                data = os.read(terminal, 4096)
            except OSError as error:
                if error.errno == errno.EIO:
                    return False
                raise
            if not data:
                return False
            output.extend(data)
        return marker in output

    try:
        assert read_until(b"SAVED_LOG_LINE", 5), f"saved logs did not render: {output!r}"
        # A split ANSI Up key starts with Esc, but must not leave a live log.
        os.write(terminal, b"\x1b")
        time.sleep(0.01)
        os.write(terminal, b"[A")
        assert not read_until(b"TAIL_VIEW_RETURNED", 0.15), "arrow exited the log view"
        os.write(terminal, close_key)
        assert read_until(b"TAIL_VIEW_RETURNED", 2), f"{close_key!r} did not return: {output!r}"
        assert b"Esc or Ctrl-C" in output, "tail view did not explain how to return"
        _, exit_status = os.waitpid(pid, 0)
        assert (
            os.waitstatus_to_exitcode(exit_status) == 0
        ), f"log viewer/tty state exit={exit_status}: {output!r}"
    finally:
        if exit_status is None:
            # First allow even the pre-fix foreground tail to restore the tty.
            try:
                os.write(terminal, b"\x03")
            except OSError:
                pass
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                exited, _ = os.waitpid(pid, os.WNOHANG)
                if exited:
                    break
                time.sleep(0.02)
            else:
                try:
                    os.killpg(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os.waitpid(pid, 0)
        os.close(terminal)


run_session(b"\x1b")
run_session(b"\x03")
