"""The outbox relay: polls for undispatched events and publishes them.

In a real deployment this would push to Kafka, see labs/kafka-messaging
for the partitions and consumer group side of that. There is no reachable
broker to publish to from this sandbox, so publish() here is a stand in,
it records what it would have sent. The guarantee this relay actually
provides is real regardless: every committed outbox row gets dispatched
at least once, including after a crash and restart, because dispatch
state lives in the same database as the events themselves.
"""

import sqlite3
from dataclasses import dataclass


@dataclass
class DispatchedEvent:
    id: int
    event_type: str
    payload: str


def fetch_undispatched(conn: sqlite3.Connection, limit: int = 100) -> list[tuple]:
    return conn.execute(
        "SELECT id, event_type, payload FROM outbox "
        "WHERE dispatched_at IS NULL ORDER BY id ASC LIMIT ?",
        (limit,),
    ).fetchall()


def mark_dispatched(conn: sqlite3.Connection, event_id: int, now: str) -> None:
    with conn:
        conn.execute(
            "UPDATE outbox SET dispatched_at = ? WHERE id = ?",
            (now, event_id),
        )


def run_once(conn: sqlite3.Connection, publish, now_fn, batch_size: int = 100, fail_after: int | None = None) -> int:
    """Process one batch of undispatched events.

    publish(event_type, payload) is called for each one; it may raise, to
    simulate the relay itself crashing mid-batch. fail_after, if set,
    raises after that many successful dispatches in this call, purely so
    tests and the demo can simulate a crash partway through a batch.
    Returns how many events were actually dispatched before returning or
    raising.
    """
    rows = fetch_undispatched(conn, limit=batch_size)
    dispatched = 0
    for event_id, event_type, payload in rows:
        if fail_after is not None and dispatched >= fail_after:
            raise RuntimeError(f"simulated relay crash after {dispatched} dispatches")
        publish(event_type, payload)
        mark_dispatched(conn, event_id, now_fn())
        dispatched += 1
    return dispatched
