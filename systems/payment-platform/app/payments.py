"""Payment creation: idempotency and the transactional outbox.

Two guarantees live here, and both are enforced by SQLite itself, not by
application level locking:

1. Idempotency: submitting the same idempotency_key twice, whether because
   a client retried after a timeout, or because two requests genuinely
   raced, returns the same payment, never a second charge. The database's
   own PRIMARY KEY constraint on idempotency_key is what actually makes
   this safe under concurrency, not the initial SELECT check, which is
   only a fast path for the common case of a clean retry after the first
   one already committed.

2. The outbox pattern: a payment and the event announcing it are written
   in one transaction. Either both exist or neither does. There is no
   window where the payment is committed but the event announcing it was
   lost, which is the dual write problem naive_create_payment below exists
   to demonstrate.
"""

import json
import sqlite3
import time
import uuid
from dataclasses import dataclass


@dataclass
class Payment:
    idempotency_key: str
    amount_cents: int
    currency: str
    status: str
    created_at: str


def _row_to_payment(row) -> Payment:
    return Payment(
        idempotency_key=row[0],
        amount_cents=row[1],
        currency=row[2],
        status=row[3],
        created_at=row[4],
    )


def get_payment(conn: sqlite3.Connection, idempotency_key: str) -> Payment | None:
    row = conn.execute(
        "SELECT idempotency_key, amount_cents, currency, status, created_at "
        "FROM payments WHERE idempotency_key = ?",
        (idempotency_key,),
    ).fetchone()
    return _row_to_payment(row) if row else None


def create_payment(conn: sqlite3.Connection, idempotency_key: str, amount_cents: int, currency: str) -> tuple[Payment, bool]:
    """Create a payment, or return the existing one for a repeated key.

    Returns (payment, created), where created is False when this call
    returned an already-existing payment instead of making a new one, so
    callers and tests can tell a fresh charge from a deduplicated retry.
    """
    existing = get_payment(conn, idempotency_key)
    if existing is not None:
        return existing, False

    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    payload = json.dumps({
        "idempotency_key": idempotency_key,
        "amount_cents": amount_cents,
        "currency": currency,
    })

    try:
        with conn:
            conn.execute(
                "INSERT INTO payments (idempotency_key, amount_cents, currency, status, created_at) "
                "VALUES (?, ?, ?, 'authorized', ?)",
                (idempotency_key, amount_cents, currency, now),
            )
            conn.execute(
                "INSERT INTO outbox (event_type, payload, created_at) VALUES (?, ?, ?)",
                ("payment.authorized", payload, now),
            )
    except sqlite3.IntegrityError:
        # Another request for the same idempotency_key committed between
        # our SELECT above and this INSERT. That request won; return what
        # it created instead of erroring, which is what makes this safe
        # under real concurrency, not just in the common sequential case.
        existing = get_payment(conn, idempotency_key)
        assert existing is not None, "insert collided but no row was found, this should not happen"
        return existing, False

    return get_payment(conn, idempotency_key), True


def naive_create_payment_no_outbox(conn: sqlite3.Connection, idempotency_key: str, amount_cents: int, currency: str, publish_fails: bool = False) -> Payment:
    """The dual write problem, on purpose: commit the payment, then
    publish the event as a separate step. If publish fails or the process
    dies in between, the payment exists and the event announcing it never
    will, silently. This function exists only to demonstrate that failure
    in the README; create_payment above is what the system actually uses.
    """
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    with conn:
        conn.execute(
            "INSERT INTO payments (idempotency_key, amount_cents, currency, status, created_at) "
            "VALUES (?, ?, ?, 'authorized', ?)",
            (idempotency_key, amount_cents, currency, now),
        )
    # The payment is already committed and durable at this point. Anything
    # from here on is a separate step that can fail independently.
    if publish_fails:
        raise RuntimeError("simulated: the publish step failed or the process died here")
    return get_payment(conn, idempotency_key)
