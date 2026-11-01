"""The payment platform as an experiment subject: how to start it, put the chaos proxy in front of it, and talk to it."""

import os
import shutil
import subprocess
import uuid
from pathlib import Path

from .client import Outcome, get_metric, request
from .procs import Proc, Stack, free_port, python

REPO = Path(__file__).resolve().parents[2]
PAYMENT_DIR = REPO / "systems" / "payment-platform"
PROXY_DIR = REPO / "chaos" / "proxy"


def build_proxy(into: Path) -> Path:
    """Builds the chaos proxy, or uses the binary CHAOSPROXY points at."""
    if os.environ.get("CHAOSPROXY"):
        return Path(os.environ["CHAOSPROXY"])
    go = shutil.which("go")
    if go is None:
        raise RuntimeError("go is needed to build the chaos proxy. Install it, or set CHAOSPROXY to a built binary.")
    out = into / "chaosproxy"
    subprocess.run([go, "build", "-o", str(out), "./cmd/chaosproxy"], cwd=PROXY_DIR, check=True)
    return out


class PaymentStack:
    """The API, optionally the relay as a separate worker, and optionally the chaos proxy in front."""

    def __init__(self, stack: Stack, *, relay: str = "embedded", publish_delay_ms: float = 0.0, proxy: bool = False):
        assert relay in ("embedded", "external")
        self.stack, self.relay_mode, self.publish_delay_ms, self.with_proxy = stack, relay, publish_delay_ms, proxy
        self.db = str(stack.workdir / "payments.db")
        self.sink = stack.workdir / "published.jsonl"
        self.api_port = free_port()
        self.api_url = f"http://127.0.0.1:{self.api_port}"
        self.api: Proc | None = None
        self.relay: Proc | None = None
        self.proxy: Proc | None = None
        self.proxy_url = self.control_url = ""

    def start(self) -> "PaymentStack":
        s = self.stack
        env = {"PAYMENT_DB_PATH": self.db, "PAYMENT_RELAY": self.relay_mode}
        self.api = s.add(Proc("api", [python(), "-m", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", str(self.api_port), "--log-level", "warning"],
                              s.workdir, env, PAYMENT_DIR)).start()
        self.api.wait_http(self.api_url + "/stats")
        if self.relay_mode == "external":
            self.relay = s.add(Proc("relay", [python(), "-m", "app.relay_worker", "--db", self.db, "--sink", str(self.sink),
                                              "--publish-delay-ms", str(self.publish_delay_ms)], s.workdir, None, PAYMENT_DIR)).start()
        if self.with_proxy:
            binary = build_proxy(s.workdir)
            listen, control = free_port(), free_port()
            self.proxy_url, self.control_url = f"http://127.0.0.1:{listen}", f"http://127.0.0.1:{control}"
            self.proxy = s.add(Proc("proxy", [str(binary), "-listen", f"127.0.0.1:{listen}", "-upstream", self.api_url, "-control", f"127.0.0.1:{control}"], s.workdir)).start()
            self.proxy.wait_http(self.control_url + "/health")
        return self

    @property
    def client_url(self) -> str:
        """Where clients should send requests: through the proxy if there is one."""
        return self.proxy_url if self.with_proxy else self.api_url

    def pay(self, key: str, amount: int, timeout: float = 2.0, direct: bool = False) -> Outcome:
        base = self.api_url if direct else self.client_url
        return request("POST", base + "/payments", {"amount_cents": amount, "currency": "usd"}, {"Idempotency-Key": key}, timeout)

    def set_fault(self, **fault) -> None:
        o = request("PUT", self.control_url + "/fault", fault)
        assert o.ok, f"the proxy rejected the fault {fault}: {o.body}"

    def clear_fault(self) -> None:
        request("DELETE", self.control_url + "/fault")

    def api_metrics(self) -> str:
        import urllib.request

        with urllib.request.urlopen(self.api_url + "/metrics", timeout=5) as r:
            return r.read().decode()

    def metric(self, name: str, **labels) -> float | None:
        return get_metric(self.api_metrics(), name, **labels)

    def restart_api(self) -> float:
        """Crashes the API with SIGKILL, starts it again on the same database and port, and returns how long it was gone."""
        died = self.api.kill9()
        self.api.start().wait_http(self.api_url + "/stats")
        import time

        return time.time() - died


def new_key() -> str:
    return str(uuid.uuid4())
