import sys
import uuid
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "systems" / "payment-platform"))

from app.db import connect, open_connection
from app.payments import create_payment
from chaos.harness.invariants import check_payments, read_state


@pytest.fixture
def db(tmp_path):
    path = str(tmp_path / "inv.db")
    connect(path).close()
    return path


def make(db_path, n):
    conn = open_connection(db_path)
    acked = {}
    for i in range(n):
        key = str(uuid.uuid4())
        create_payment(conn, key, 100 + i, "usd")
        acked[key] = 100 + i
    conn.close()
    return acked


def sql(db_path, statement, *params):
    conn = open_connection(db_path)
    with conn:
        conn.execute(statement, params)
    conn.close()


def test_a_healthy_database_has_no_violations(db):
    acked = make(db, 10)
    r = check_payments(db, acked, set(acked))
    assert r.ok, r.violations
    assert r.counts["payments"] == r.counts["events"] == 10 and r.counts["pending"] == 10 and r.counts["in_doubt"] == 0


def test_an_acknowledged_payment_that_is_missing_is_a_lost_commit(db):
    acked = make(db, 5)
    victim = next(iter(acked))
    sql(db, "DELETE FROM payments WHERE idempotency_key = ?", victim)
    sql(db, "DELETE FROM outbox WHERE payload LIKE ?", f"%{victim}%")
    r = check_payments(db, acked, set(acked))
    assert any("lost" in v and victim in v for v in r.violations)


def test_a_changed_amount_is_caught(db):
    acked = make(db, 3)
    victim = next(iter(acked))
    sql(db, "UPDATE payments SET amount_cents = 999999 WHERE idempotency_key = ?", victim)
    assert any("999999" in v for v in check_payments(db, acked, set(acked)).violations)


def test_a_payment_the_client_never_sent_is_a_phantom(db):
    acked = make(db, 3)
    extra = make(db, 1)
    r = check_payments(db, acked, set(acked))
    assert any("never sent" in v and next(iter(extra)) in v for v in r.violations)


def test_a_payment_without_an_event_committed_without_being_announced(db):
    acked = make(db, 3)
    victim = next(iter(acked))
    sql(db, "DELETE FROM outbox WHERE payload LIKE ?", f"%{victim}%")
    assert any("no outbox event" in v for v in check_payments(db, acked, set(acked)).violations)


def test_a_duplicate_event_is_caught(db):
    acked = make(db, 3)
    victim = next(iter(acked))
    sql(db, "INSERT INTO outbox (event_type, payload, created_at) SELECT event_type, payload, created_at FROM outbox WHERE payload LIKE ?", f"%{victim}%")
    assert any("2 outbox events" in v for v in check_payments(db, acked, set(acked)).violations)


def test_an_event_without_a_payment_is_caught(db):
    acked = make(db, 2)
    sql(db, "INSERT INTO outbox (event_type, payload, created_at) VALUES ('payment.authorized', '{\"idempotency_key\": \"ghost\"}', 'now')")
    assert any("ghost" in v and "did not happen" in v for v in check_payments(db, acked, set(acked)).violations)


def test_in_doubt_means_executed_but_never_acknowledged(db):
    acked = make(db, 4)
    unacked = dict(list(acked.items())[:2])
    still_acked = {k: v for k, v in acked.items() if k not in unacked}
    r = check_payments(db, still_acked, set(acked))
    assert r.ok and r.counts["in_doubt"] == 2


def test_state_is_read_without_taking_a_write_lock(db):
    make(db, 3)
    holder = open_connection(db)
    holder.execute("BEGIN IMMEDIATE")  # another process holds the write lock
    payments, events, pending, dispatched = read_state(db)
    holder.execute("ROLLBACK")
    holder.close()
    assert len(payments) == 3 and pending == 3
