"""The payment platform's HTTP API.

Two endpoints: POST /payments to create one (idempotent on the
Idempotency-Key header, the same header name Stripe's real API uses for
this), and GET /payments/{key} to look one up. A background thread runs
the outbox relay continuously, see outbox_relay.py.
"""

import os
import threading
import time

from fastapi import FastAPI, Header, HTTPException
from pydantic import BaseModel

from app.db import connect
from app.payments import create_payment, get_payment
from app.outbox_relay import run_once

DB_PATH = os.environ.get("PAYMENT_DB_PATH", "/tmp/payments.db")

app = FastAPI(title="Faultline Payment Platform")
conn = connect(DB_PATH)

published_log: list[tuple[str, str]] = []
_stop_relay = threading.Event()


def _relay_loop():
    while not _stop_relay.is_set():
        run_once(conn, publish=lambda t, p: published_log.append((t, p)), now_fn=lambda: time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        time.sleep(0.1)


_relay_thread = threading.Thread(target=_relay_loop, daemon=True)
_relay_thread.start()


class CreatePaymentRequest(BaseModel):
    amount_cents: int
    currency: str


@app.post("/payments")
def create(request: CreatePaymentRequest, idempotency_key: str = Header(..., alias="Idempotency-Key")):
    payment, created = create_payment(conn, idempotency_key, request.amount_cents, request.currency)
    return {
        "idempotency_key": payment.idempotency_key,
        "amount_cents": payment.amount_cents,
        "currency": payment.currency,
        "status": payment.status,
        "created": created,  # False means this was a deduplicated retry, not a new charge
    }


@app.get("/payments/{idempotency_key}")
def get(idempotency_key: str):
    payment = get_payment(conn, idempotency_key)
    if payment is None:
        raise HTTPException(status_code=404)
    return {
        "idempotency_key": payment.idempotency_key,
        "amount_cents": payment.amount_cents,
        "currency": payment.currency,
        "status": payment.status,
    }


@app.get("/stats")
def stats():
    pending = conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NULL").fetchone()[0]
    dispatched = conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NOT NULL").fetchone()[0]
    total_payments = conn.execute("SELECT COUNT(*) FROM payments").fetchone()[0]
    return {
        "total_payments": total_payments,
        "outbox_pending": pending,
        "outbox_dispatched": dispatched,
        "published_log_len": len(published_log),
    }
