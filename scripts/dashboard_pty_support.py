"""PTY lifecycle helpers for the dashboard acceptance script."""

import fcntl
import os
import pty
import select
import signal
import struct
import termios
import time


REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENTRY = os.path.join(REPO, "bin", "whirlwind")
RENDER_MARKER = b"Projects"


class PTYSession:
    def __init__(self, env, argv=(ENTRY, "dashboard"), cwd=REPO):
        self.pid, self.master = pty.fork()
        if self.pid == 0:
            os.chdir(cwd)
            os.execve(argv[0], argv, env)
        fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 112, 0, 0))
        self.output = bytearray()
        self.reaped = False

    def child_status(self, options):
        try:
            waited, status = os.waitpid(self.pid, options)
        except ChildProcessError:
            return 0
        if waited:
            self.reaped = True
            return status
        return None

    def read_available(self, timeout):
        ready, _, _ = select.select([self.master], [], [], timeout)
        if not ready:
            return True
        try:
            chunk = os.read(self.master, 65536)
        except OSError:
            return False
        if not chunk:
            return False
        self.output.extend(chunk)
        return True

    def terminate(self):
        # pty.fork gives this test its own session/process group. Terminate the
        # whole owned launcher group, drain output and never wait indefinitely.
        for requested_signal in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(self.pid, requested_signal)
            except ProcessLookupError:
                pass
            deadline = time.monotonic() + 2
            while time.monotonic() < deadline:
                status = self.child_status(os.WNOHANG)
                if status is not None:
                    return status
                self.read_available(0.05)
                time.sleep(0.01)
        raise RuntimeError(f"owned PTY process group {self.pid} did not reap; output={bytes(self.output)[-4000:]!r}")

    def wait_for_render(self):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline and RENDER_MARKER not in self.output:
            if not self.read_available(0.2):
                break
            result = self.child_status(os.WNOHANG)
            if result is not None:
                code = exit_code(result)
                raise AssertionError(f"dashboard failed to render in PTY, exit={code}: {bytes(self.output)[-4000:]!r}")
        if RENDER_MARKER not in self.output:
            status = self.terminate()
            raise AssertionError(f"dashboard failed to render in PTY, exit={exit_code(status)}: {bytes(self.output)[-4000:]!r}")

    def quit_and_wait(self):
        os.write(self.master, b"q")
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            result = self.child_status(os.WNOHANG)
            if result is not None:
                return exit_code(result)
            self.read_available(0.1)
            time.sleep(0.01)
        status = self.terminate()
        raise AssertionError(f"dashboard remained open after q, exit={exit_code(status)}")

    def close(self):
        try:
            if not self.reaped:
                self.terminate()
        finally:
            os.close(self.master)


def dashboard_pty(env):
    session = PTYSession(env)
    try:
        session.wait_for_render()
        code = session.quit_and_wait()
        if code != 0:
            raise AssertionError(f"dashboard did not exit cleanly after q (exit={code}): {bytes(session.output)[-4000:]!r}")
        for marker in (b"Projects", b"Agents", b"Runs", b"Inbox", b"No projects"):
            if marker not in session.output:
                raise AssertionError(f"dashboard PTY output missed {marker!r}: {bytes(session.output)[-4000:]!r}")
        return bytes(session.output)
    finally:
        session.close()


def exit_code(status):
    if os.WIFEXITED(status):
        return os.WEXITSTATUS(status)
    if os.WIFSIGNALED(status):
        return 128 + os.WTERMSIG(status)
    return status
