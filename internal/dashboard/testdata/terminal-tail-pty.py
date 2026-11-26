"""Exercise saved tail output, close keys, and tty state restoration."""

import errno
import os
import pty
import select
import signal
import sys
import time


def run_session(close_key):
    pid, terminal = pty.fork()
    if pid == 0:
        os.execv(sys.argv[1], [sys.argv[1], "-test.run=^TestTerminalTailKeysInRealPTY$"])

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
        # ANSI Up must not leave a live log. Split reads are tested in Go
        # so this check does not depend on scheduling two separate writes.
        os.write(terminal, b"\x1b[A")
        assert not read_until(b"TAIL_VIEW_RETURNED", 0.15), "arrow exited the log view"
        os.write(terminal, close_key)
        assert read_until(b"TAIL_VIEW_RETURNED", 2), f"{close_key!r} did not return: {output!r}"
        assert b"Esc or Ctrl-C" in output, "tail view did not explain how to return"
        _, exit_status = os.waitpid(pid, 0)
        # Keep the PTY open and retain any final race-detector or tty diagnostics.
        read_until(b"\x00", 0.25)
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
