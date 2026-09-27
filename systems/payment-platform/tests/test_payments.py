import sys
import threading
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import pytest

from app.db import connect
from app.payments import create_payment, get_payment, naive_create_payment_no_outbox


@pytest.fixture
def conn(tmp_path):
    c = connect(str(tmp_path / "test.db"))
    yield c
    c.close()


def test_create_payment_creates_new(conn):
    key = str(uuid.uuid4())
    payment, created = create_payment(conn, key, 5000, "usd")
    assert created is True
    assert payment.amount_cents == 5000
    assert payment.status == "authorized"


def test_create_payment_same_key_returns_existing(conn):
    key = str(uuid.uuid4())
    first, first_created = create_payment(conn, key, 5000, "usd")
    second, second_created = create_payment(conn, key, 9999, "eur")  # deliberately different amount

    assert first_created is True
    assert second_created is False
    assert second.amount_cents == 5000, "the original amount must win, not the retry's, whatever it happened to send"
    assert second.currency == "usd"


def test_create_payment_writes_outbox_event_atomically(conn):
    key = str(uuid.uuid4())
    create_payment(conn, key, 1234, "usd")

    events = conn.execute("SELECT event_type, payload FROM outbox").fetchall()
    assert len(events) == 1
    assert events[0][0] == "payment.authorized"
    assert key in events[0][1]


def test_concurrent_same_key_only_creates_once(conn):
    # This is the scenario that matters: a client's request timed out, so
    # it retries, but the original request actually succeeded and is still
    # in flight. Both arrive at roughly the same instant with the same
    # idempotency key.
    key = str(uuid.uuid4())
    results = []
    lock = threading.Lock()

    def attempt():
        payment, created = create_payment(conn, key, 5000, "usd")
        with lock:
            results.append((payment.idempotency_key, created))

    threads = [threading.Thread(target=attempt) for _ in range(20)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    created_count = sum(1 for _, created in results if created)
    assert created_count == 1, f"expected exactly 1 of 20 concurrent identical requests to actually create a payment, got {created_count}"

    row_count = conn.execute("SELECT COUNT(*) FROM payments WHERE idempotency_key = ?", (key,)).fetchone()[0]
    assert row_count == 1

    event_count = conn.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
    assert event_count == 1, f"expected exactly 1 outbox event despite 20 concurrent attempts, got {event_count}"


def test_naive_no_outbox_loses_event_on_publish_failure(conn):
    key = str(uuid.uuid4())

    with pytest.raises(RuntimeError):
        naive_create_payment_no_outbox(conn, key, 5000, "usd", publish_fails=True)

    # The payment is there, committed for real, but nothing recorded that
    # an event should ever be published for it. This is the dual write
    # problem: create_payment's outbox-based version cannot lose an event
    # this way, since the event is committed in the same transaction as
    # the payment, not as a separate step afterward.
    payment = get_payment(conn, key)
    assert payment is not None, "the payment itself should still be committed"

    events = conn.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
    assert events == 0, "no outbox row exists for this payment, the naive path has no such row to begin with"
