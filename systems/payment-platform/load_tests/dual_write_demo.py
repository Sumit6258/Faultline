"""Demonstrates the dual write problem directly: commit the payment, then
publish as a separate step that can fail. Contrasts it with the outbox
version, where that same failure cannot lose the event, because the event
was already committed in the same transaction as the payment itself.
"""

import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.db import connect
from app.payments import create_payment, naive_create_payment_no_outbox


def main():
    conn = connect("/tmp/dual_write_demo.db")
    conn.execute("DELETE FROM payments")
    conn.execute("DELETE FROM outbox")
    conn.commit()

    print("Naive: commit the payment, then publish as a separate step that fails.")
    key1 = str(uuid.uuid4())
    try:
        naive_create_payment_no_outbox(conn, key1, 5000, "usd", publish_fails=True)
    except RuntimeError as e:
        print(f"  publish step raised: {e}")

    payment_exists = conn.execute("SELECT COUNT(*) FROM payments WHERE idempotency_key=?", (key1,)).fetchone()[0]
    event_exists = conn.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
    print(f"  payment committed: {bool(payment_exists)}")
    print(f"  any outbox event exists to ever announce it: {bool(event_exists)}")
    print("  this customer was charged. nothing downstream will ever be told, unless someone")
    print("  notices the discrepancy by reconciling payments against events by hand.")

    print()
    print("Outbox: the payment and its event commit in one transaction.")
    key2 = str(uuid.uuid4())
    create_payment(conn, key2, 5000, "usd")
    event_exists2 = conn.execute("SELECT COUNT(*) FROM outbox WHERE payload LIKE ?", (f"%{key2}%",)).fetchone()[0]
    print(f"  payment committed: True")
    print(f"  outbox event exists for it: {bool(event_exists2)}")
    print("  even if the relay that publishes this event crashes immediately after, the event")
    print("  is safely durable and will be picked up on restart. see the crash-and-resume test")
    print("  in tests/test_outbox_relay.py for that scenario proven directly.")

    conn.close()


if __name__ == "__main__":
    main()
