"""SQLite schema and connection helpers.

SQLite, not Postgres, is the store for this system. It is a real,
transactional SQL engine, not a simulation, and unlike Postgres in this
sandbox it needs no long running server process to stay alive between
separate commands, which is exactly the constraint that made the
replication lab's real Postgres attempt unreliable here. Everything the
outbox pattern actually depends on, atomic multi-statement transactions,
a unique constraint enforced by the engine itself, is real in SQLite too.
"""

import sqlite3

SCHEMA = """
CREATE TABLE IF NOT EXISTS payments (
    idempotency_key TEXT PRIMARY KEY,
    amount_cents    INTEGER NOT NULL,
    currency        TEXT NOT NULL,
    status          TEXT NOT NULL,
    created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type     TEXT NOT NULL,
    payload        TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    dispatched_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_undispatched
    ON outbox (dispatched_at)
    WHERE dispatched_at IS NULL;
"""


def connect(path: str) -> sqlite3.Connection:
    """Open a connection with the schema applied.

    check_same_thread=False because FastAPI's threadpool for sync path
    functions can hand a request to any worker thread; isolation_level
    left at its default so `with conn:` blocks commit or roll back the
    whole block atomically, which is what the outbox pattern needs.
    """
    conn = sqlite3.connect(path, check_same_thread=False)
    conn.execute("PRAGMA journal_mode = WAL;")
    conn.executescript(SCHEMA)
    conn.commit()
    return conn
