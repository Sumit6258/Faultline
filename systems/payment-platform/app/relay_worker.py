"""Runs the outbox relay as its own process.

By default the API process runs the relay on a thread, which is the simplest
thing that works. Running it separately, with PAYMENT_RELAY=external on the
API, is how most outbox deployments look: the API only ever writes to the
database, and a worker publishes. It also lets the relay fail, stall or be
restarted on its own, which is what chaos/ does to it.

    python -m app.relay_worker --db payments.db --sink published.jsonl

Each event is appended to the sink as one JSON line, then marked as
dispatched. A crash between the two leaves the event undispatched, so it is
published again on restart: at least once, never zero times. The sink stands
in for a broker, and makes that visible from outside the process. Whatever
consumes the real broker has to tolerate seeing an event twice, which is the
job of the idempotent consumer in labs/kafka-messaging.
"""

import argparse
import json
import signal
import sys
import time

from app.db import open_connection
from app.outbox_relay import run_once


class _Stop(Exception):
    """Raised between events when a stop was requested, never in the middle of one."""


def _now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def make_publisher(sink, publish_delay: float, should_stop=lambda: False):
    """Returns a publish(event_type, payload) that appends to sink.

    publish_delay stands in for the round trip to a broker: half of it passes
    before the broker accepts the event, which is written to the sink, and
    half after, before the acknowledgement gets back. The second half is the
    window a crash has to land in to cause a duplicate, and the first is the
    window in which a relay that marked events before publishing them would
    lose one. With no delay both windows are real but tiny.
    """

    def publish(event_type: str, payload: str) -> None:
        if should_stop():
            raise _Stop()
        event = json.loads(payload)
        if publish_delay:
            time.sleep(publish_delay / 2)
        sink.write(json.dumps({"event_type": event_type, "idempotency_key": event["idempotency_key"], "published_at": time.time()}) + "\n")
        sink.flush()
        if publish_delay:
            time.sleep(publish_delay / 2)

    return publish


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", required=True, help="path to the payment platform's SQLite database")
    ap.add_argument("--sink", required=True, help="file that published events are appended to, one JSON object per line")
    ap.add_argument("--interval", type=float, default=0.1, help="seconds between polls when nothing was waiting")
    ap.add_argument("--batch", type=int, default=100)
    ap.add_argument("--publish-delay-ms", type=float, default=0.0, help="simulated broker acknowledgement time")
    ap.add_argument("--once", action="store_true", help="drain what is waiting, then exit")
    args = ap.parse_args(argv)

    stopping = False

    def request_stop(signum, frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)

    conn = open_connection(args.db)
    try:
        with open(args.sink, "a", buffering=1) as sink:
            publish = make_publisher(sink, args.publish_delay_ms / 1000.0, lambda: stopping)
            try:
                while not stopping:
                    n = run_once(conn, publish=publish, now_fn=_now, batch_size=args.batch)
                    if args.once and n == 0:
                        break
                    if n == 0:
                        time.sleep(args.interval)
            except _Stop:
                pass
    finally:
        conn.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
