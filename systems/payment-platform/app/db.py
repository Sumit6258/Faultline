"""SQLite schema and connection helpers.

SQLite, not Postgres, is the store for this system. It is a real,
transactional SQL engine, not a simulation, and unlike Postgres in this
sandbox it needs no long running server process to stay alive between
separate commands, which is exactly the constraint that made the
replication lab's real Postgres attempt unreliable here. Everything the
outbox pattern actually depends on, atomic multi-statement transactions,
a unique constraint enforced by the engine itself, is real in SQLite too.

One rule governs every connection in this system: a connection belongs to
one thread of work at a time. A sqlite3 connection holds the transaction
state, not the thread using it, so two threads sharing a connection share
a single transaction. One thread's commit() commits the other's half
finished work, and one thread's rollback() throws it away. The first
version of this system shared one connection across all request threads
and the relay, and it failed in CI, see load_tests/shared_connection_race.py
for the measured damage. Every request, test thread, and background loop
now opens its own connection, which is cheap in SQLite, and which makes the
database itself, not Python, the arbiter between concurrent writers.
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

# How long a writer waits for another writer's lock before giving up. Writers
# in SQLite take turns, so under contention a request waits briefly instead of
# failing, which is the behavior a retrying client would otherwise have to
# paper over.
BUSY_TIMEOUT_SECONDS = 10.0


def open_connection(path: str) -> sqlite3.Connection:
    """Open one connection to an existing database, with no schema work.

    check_same_thread=False only because FastAPI may create a request's
    connection on one worker thread and use it on another. The connection
    is still never used by two threads at the same time, which is the rule
    that matters. isolation_level is left at its default so `with conn:`
    blocks commit or roll back the whole block atomically, which is what the
    outbox pattern needs.
    """
    return sqlite3.connect(path, timeout=BUSY_TIMEOUT_SECONDS, check_same_thread=False)


def init_db(path: str) -> None:
    """Create the schema and switch the database file to WAL mode.

    Run once at startup. WAL mode is a property of the database file, so
    every connection opened afterwards inherits it.
    """
    conn = open_connection(path)
    try:
        conn.execute("PRAGMA journal_mode = WAL;")
        conn.executescript(SCHEMA)
        conn.commit()
    finally:
        conn.close()


def connect(path: str) -> sqlite3.Connection:
    """Initialize the database if needed, then open a connection to it.

    Convenient for tests, scripts and benchmarks that use one connection
    from one thread. Code that runs on several threads should call
    init_db once and then open_connection in each thread.
    """
    init_db(path)
    return open_connection(path)
