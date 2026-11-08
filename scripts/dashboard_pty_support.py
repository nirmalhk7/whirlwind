"""PTY lifecycle helpers for the dashboard acceptance script."""

import fcntl
import os
import pty
import select
import struct
import termios
import time


REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENTRY = os.path.join(REPO, "bin", "whirlwind")
RENDER_MARKER = b"Whirlwind | local terminal dashboard"


class PTYSession:
    def __init__(self, env):
        self.pid, self.master = pty.fork()
        if self.pid == 0:
            os.chdir(REPO)
            os.execve(ENTRY, [ENTRY, "dashboard"], env)
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
        os.kill(self.pid, 15)
        _, status = os.waitpid(self.pid, 0)
        self.reaped = True
        return status

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
                code = exit_code(result)
                break
            if not self.read_available(0.1):
                break
        else:
            status = self.terminate()
            raise AssertionError(f"dashboard remained open after q, exit={exit_code(status)}")
        if not self.reaped:
            _, status = os.waitpid(self.pid, 0)
            self.reaped = True
            code = exit_code(status)
        return code

    def close(self):
        if not self.reaped:
            os.kill(self.pid, 15)
            os.waitpid(self.pid, 0)
        os.close(self.master)


def dashboard_pty(env):
    session = PTYSession(env)
    try:
        session.wait_for_render()
        code = session.quit_and_wait()
        if code != 0:
            raise AssertionError(f"dashboard did not exit cleanly after q (exit={code}): {bytes(session.output)[-4000:]!r}")
        for marker in (b"Projects", b"Plans", b"No projects"):
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
