"""Concurrency tests: many threads, each with its own connection.

These exist because the first version of this system shared one SQLite
connection across threads, and nothing in a single threaded test run could
see the problem. See app/db.py and load_tests/shared_connection_race.py.
"""

import sys
import threading
import uuid
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.db import connect, open_connection
from app.outbox_relay import run_once
from app.payments import create_payment


@pytest.fixture
def db_path(tmp_path):
    path = str(tmp_path / "test.db")
    connect(path).close()
    return path


def test_repeated_rounds_of_identical_concurrent_requests_never_double_charge(db_path):
    rounds, n_threads = 30, 20
    errors = []
    lock = threading.Lock()

    for _ in range(rounds):
        key = str(uuid.uuid4())
        created_flags = []
        start_together = threading.Barrier(n_threads)

        def attempt():
            own = open_connection(db_path)
            try:
                start_together.wait()
                _, created = create_payment(own, key, 100, "usd")
                with lock:
                    created_flags.append(created)
            except Exception as e:
                with lock:
                    errors.append(repr(e))
            finally:
                own.close()

        threads = [threading.Thread(target=attempt) for _ in range(n_threads)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert sum(created_flags) == 1, f"round with key {key}: {sum(created_flags)} requests believed they created the payment"

    assert not errors, f"{len(errors)} requests raised, first: {errors[0]}"

    check = open_connection(db_path)
    try:
        assert check.execute("SELECT COUNT(*) FROM payments").fetchone()[0] == rounds
        assert check.execute("SELECT COUNT(*) FROM outbox").fetchone()[0] == rounds
    finally:
        check.close()


def test_writers_and_relay_running_together_keep_payments_and_events_in_step(db_path):
    """Request threads and the relay each have their own connection, so the
    relay's commits can never commit a request's half finished transaction.

    Every payment must end with exactly one event, and the relay must publish
    every event exactly once, with nothing raising along the way.
    """
    n_writers, per_writer = 6, 40
    errors = []
    published = []
    lock = threading.Lock()
    writers_done = threading.Event()

    def writer(worker: int):
        own = open_connection(db_path)
        try:
            for i in range(per_writer):
                create_payment(own, f"w{worker}-{i}-{uuid.uuid4()}", 100, "usd")
        except Exception as e:
            with lock:
                errors.append(repr(e))
        finally:
            own.close()

    def relay():
        own = open_connection(db_path)
        try:
            while True:
                # Read the flag before the pass. If every writer had already
                # finished when this pass began and the pass found nothing
                # left, the outbox is fully drained. run_once handles one batch
                # per call, so a single final pass would leave a backlog.
                finished = writers_done.is_set()
                dispatched = run_once(own, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "2026-01-01T00:00:00Z")
                if finished and dispatched == 0:
                    break
        except Exception as e:
            with lock:
                errors.append(repr(e))
        finally:
            own.close()

    relay_thread = threading.Thread(target=relay)
    writer_threads = [threading.Thread(target=writer, args=(w,)) for w in range(n_writers)]
    relay_thread.start()
    for t in writer_threads:
        t.start()
    for t in writer_threads:
        t.join()
    writers_done.set()
    relay_thread.join()

    assert not errors, f"{len(errors)} errors, first: {errors[0]}"

    total = n_writers * per_writer
    check = open_connection(db_path)
    try:
        payments = check.execute("SELECT COUNT(*) FROM payments").fetchone()[0]
        events = check.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
        undispatched = check.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NULL").fetchone()[0]
    finally:
        check.close()

    assert payments == total
    assert events == total, "every payment must have exactly one event"
    assert undispatched == 0
    assert len(published) == total, "the relay must publish each event exactly once"


def test_open_connection_returns_independent_connections(db_path):
    """The rule everything else rests on, checked directly and without any
    timing: a commit on one connection must not commit another connection's
    half finished transaction. If open_connection ever hands out a shared or
    cached connection, this fails every time, not one run in ten.
    """
    request = open_connection(db_path)
    relay = open_connection(db_path)
    observer = open_connection(db_path)
    try:
        # The first statement of a request's transaction, not yet committed.
        request.execute(
            "INSERT INTO payments (idempotency_key, amount_cents, currency, status, created_at) "
            "VALUES ('k1', 100, 'usd', 'authorized', 'now')"
        )
        relay.commit()  # the relay committing its own work, on its own connection

        assert observer.execute("SELECT COUNT(*) FROM payments").fetchone()[0] == 0, (
            "another connection's commit made this request's uncommitted insert durable"
        )

        request.commit()
        assert observer.execute("SELECT COUNT(*) FROM payments").fetchone()[0] == 1
    finally:
        for c in (request, relay, observer):
            c.close()
