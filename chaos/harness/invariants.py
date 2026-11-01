"""What must be true of the payment platform's data, however it got there.

These read the database directly, read only, after an experiment, rather than
asking the service what it believes. A service that has just been crashed and
restarted is exactly the thing whose own account of itself should not be
taken on trust.
"""

import collections
import json
import sqlite3
from dataclasses import dataclass, field


@dataclass
class Report:
    violations: list[str] = field(default_factory=list)
    counts: dict = field(default_factory=dict)

    @property
    def ok(self) -> bool:
        return not self.violations


def read_state(db_path: str):
    """Returns (payments {key: amount}, events {key: how many}, pending, dispatched)."""
    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=10)
    try:
        payments = dict(conn.execute("SELECT idempotency_key, amount_cents FROM payments"))
        events = collections.Counter(json.loads(p)["idempotency_key"] for (p,) in conn.execute("SELECT payload FROM outbox"))
        pending = conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NULL").fetchone()[0]
        dispatched = conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NOT NULL").fetchone()[0]
    finally:
        conn.close()
    return payments, events, pending, dispatched


def check_payments(db_path: str, acked: dict[str, int], attempted: set[str] | None = None) -> Report:
    """
    acked      keys the client was told succeeded, with the amount it asked for.
    attempted  every key the client ever sent. Anything else in the table is a phantom.
    """
    payments, events, pending, dispatched = read_state(db_path)
    r = Report()

    for key, amount in acked.items():
        if key not in payments:
            r.violations.append(f"acknowledged payment {key} is not in the database: a durable commit was lost")
        elif payments[key] != amount:
            r.violations.append(f"payment {key} holds {payments[key]} cents but the client asked for {amount}")

    if attempted is not None:
        for key in payments:
            if key not in attempted:
                r.violations.append(f"payment {key} exists but the client never sent that key")

    for key in payments:
        n = events.get(key, 0)
        if n == 0:
            r.violations.append(f"payment {key} has no outbox event: it committed without being announced")
        elif n > 1:
            r.violations.append(f"payment {key} has {n} outbox events, the world would hear about it {n} times")
    for key in events:
        if key not in payments:
            r.violations.append(f"outbox event for {key} but no such payment: an announcement of something that did not happen")

    r.counts = {
        "payments": len(payments),
        "events": sum(events.values()),
        "pending": pending,
        "dispatched": dispatched,
        "acked": len(acked),
        "in_doubt": len([k for k in (attempted or set()) if k in payments and k not in acked]),
    }
    return r
