"""Starting, stopping, crashing and pausing the processes an experiment runs.

Faults are applied from outside, the way a real failure would arrive: a
process is killed with SIGKILL and gets no chance to clean up, or frozen with
SIGSTOP and thawed with SIGCONT. Nothing here asks a service to cooperate.
"""

import contextlib
import os
import signal
import socket
import subprocess
import sys
import time
import urllib.request
from pathlib import Path


def free_port() -> int:
    """A port nothing is listening on, so experiments never collide with each other or with services you have running."""
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class Proc:
    def __init__(self, name: str, argv: list[str], logdir: Path, env: dict | None = None, cwd: Path | None = None):
        self.name, self.argv, self.cwd = name, argv, cwd
        self.env = {**os.environ, **(env or {})}
        self.logpath = logdir / f"{name}.log"
        self._p: subprocess.Popen | None = None
        self.starts = 0

    def start(self) -> "Proc":
        assert not self.running(), f"{self.name} is already running"
        log = open(self.logpath, "ab")
        self._p = subprocess.Popen(self.argv, cwd=self.cwd, env=self.env, stdout=log, stderr=subprocess.STDOUT)
        self.starts += 1
        return self

    @property
    def pid(self) -> int:
        assert self._p is not None
        return self._p.pid

    def running(self) -> bool:
        return self._p is not None and self._p.poll() is None

    def wait_http(self, url: str, timeout: float = 30.0) -> "Proc":
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not self.running():
                raise RuntimeError(f"{self.name} exited early with {self._p.returncode}, see {self.logpath}")
            try:
                with urllib.request.urlopen(url, timeout=1) as r:
                    if r.status == 200:
                        return self
            except Exception:
                time.sleep(0.1)
        raise TimeoutError(f"{self.name} did not answer {url} within {timeout}s, see {self.logpath}")

    def kill9(self) -> float:
        """SIGKILL: the process cannot clean up, flush or finish what it was doing. Returns when it died."""
        assert self.running(), f"{self.name} is not running"
        self._p.kill()
        self._p.wait()
        return time.time()

    def terminate(self, timeout: float = 5.0) -> None:
        if not self.running():
            return
        self._p.terminate()
        try:
            self._p.wait(timeout)
        except subprocess.TimeoutExpired:
            self._p.kill()
            self._p.wait()

    def pause(self) -> None:
        """SIGSTOP: the process still exists, and does nothing at all, as in a long GC pause or a hung host."""
        os.kill(self.pid, signal.SIGSTOP)

    def resume(self) -> None:
        os.kill(self.pid, signal.SIGCONT)

    def restart(self) -> "Proc":
        self.terminate()
        return self.start()


class Stack(contextlib.ExitStack):
    """Owns every process an experiment starts and guarantees they are stopped, even if the experiment fails."""

    def __init__(self, workdir: Path):
        super().__init__()
        self.workdir = workdir
        workdir.mkdir(parents=True, exist_ok=True)
        self.procs: list[Proc] = []

    def add(self, proc: Proc) -> Proc:
        self.procs.append(proc)
        self.callback(self._stop, proc)
        return proc

    @staticmethod
    def _stop(proc: Proc) -> None:
        if proc._p is not None and proc.running():
            with contextlib.suppress(ProcessLookupError):
                os.kill(proc.pid, signal.SIGCONT)  # a paused process cannot be terminated until it is resumed
            proc.terminate()


def python() -> str:
    return sys.executable
