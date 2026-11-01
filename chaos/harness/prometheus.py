"""An optional real Prometheus, so an experiment can check that an alert fires for a real fault.

It uses the repository's own alert rules, with only the "for" delays shortened
to five seconds so an experiment need not wait a quarter of an hour.
"""

import json
import re
import shutil
import time
import urllib.request
from pathlib import Path

from .procs import Proc, Stack, free_port

REPO = Path(__file__).resolve().parents[2]


class PrometheusRun:
    def __init__(self, stack: Stack, binary: str, job: str, target_port: int):
        d = stack.workdir / "prometheus"
        (d / "rules").mkdir(parents=True)
        for f in (REPO / "observability" / "prometheus" / "rules").glob("*.yml"):
            text = re.sub(r"^( *for: )\d+m$", r"\g<1>5s", f.read_text(), flags=re.M)
            (d / "rules" / f.name).write_text(text)
        (d / "prometheus.yml").write_text(
            "global:\n  scrape_interval: 1s\n  evaluation_interval: 1s\n"
            "rule_files:\n  - rules/*.yml\n"
            f"scrape_configs:\n  - job_name: {job}\n    static_configs:\n      - targets: ['127.0.0.1:{target_port}']\n"
        )
        port = free_port()
        self.url = f"http://127.0.0.1:{port}"
        self.proc = stack.add(
            Proc("prometheus", [binary, f"--config.file={d / 'prometheus.yml'}", f"--storage.tsdb.path={d / 'data'}",
                                f"--web.listen-address=127.0.0.1:{port}", "--log.level=warn"], stack.workdir)
        ).start()
        self.proc.wait_http(self.url + "/-/ready")

    def alerts(self) -> dict[str, str]:
        """alert name -> state ("pending" or "firing"), for every alert that is active now."""
        with urllib.request.urlopen(self.url + "/api/v1/alerts", timeout=5) as r:
            data = json.load(r)["data"]["alerts"]
        return {a["labels"]["alertname"]: a["state"] for a in data}

    def wait_for(self, alert: str, state: str | None, timeout: float) -> float | None:
        """Waits until `alert` is in `state` (None means not active at all). Returns seconds waited, or None on timeout."""
        start = time.time()
        while time.time() - start < timeout:
            if self.alerts().get(alert) == state:
                return time.time() - start
            time.sleep(0.5)
        return None
