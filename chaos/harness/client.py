"""A small HTTP client that reports what a failure looked like from outside.

A client does not see "the server crashed" or "the response was dropped". It
sees a timeout, a closed connection, an error status or an answer, and has to
decide what to do from that alone. This sorts what it sees into those cases.
"""

import http.client
import json
import socket
import time
import urllib.error
import urllib.request
from dataclasses import dataclass


@dataclass
class Outcome:
    kind: str  # "ok", "http_error", "timeout", "conn_error"
    status: int | None
    body: dict | None
    elapsed: float
    error: str = ""

    @property
    def ok(self) -> bool:
        return self.kind == "ok"

    @property
    def answered(self) -> bool:
        """The client got an answer, whatever it said."""
        return self.kind in ("ok", "http_error")


def _json(raw: bytes):
    try:
        return json.loads(raw)
    except Exception:
        return None


def request(method: str, url: str, body=None, headers=None, timeout: float = 2.0) -> Outcome:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    start = time.perf_counter()

    def done(kind, status=None, parsed=None, error=""):
        return Outcome(kind, status, parsed, time.perf_counter() - start, error)

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return done("ok", resp.status, _json(resp.read()))
    except urllib.error.HTTPError as e:
        return done("http_error", e.code, _json(e.read()), str(e))
    except (socket.timeout, TimeoutError) as e:
        return done("timeout", error=str(e))
    except urllib.error.URLError as e:
        if isinstance(e.reason, (socket.timeout, TimeoutError)):
            return done("timeout", error=str(e))
        return done("conn_error", error=str(e.reason))
    except (http.client.RemoteDisconnected, http.client.IncompleteRead, ConnectionError, OSError) as e:
        return done("conn_error", error=f"{type(e).__name__}: {e}")


def get_metric(metrics_text: str, name: str, **labels) -> float | None:
    """One sample from a Prometheus text page, by name and exact labels. None if absent."""
    want = {f'{k}="{v}"' for k, v in labels.items()}
    for line in metrics_text.splitlines():
        if line.startswith("#") or not line.startswith(name):
            continue
        head, _, value = line.rpartition(" ")
        base, _, rest = head.partition("{")
        if base != name:
            continue
        present = set(filter(None, rest.rstrip("}").split(","))) if rest else set()
        if present == want:
            return float(value)
    return None
