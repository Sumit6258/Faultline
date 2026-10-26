"""Why a SQLite connection must not be shared across threads.

The first version of this system opened one connection at startup and
handed it to every request thread and to the relay. It passed every test and
the HTTP demo on a single core, and failed in CI. This script reproduces
both halves of the problem, side by side with the fix.

Part 1: 20 threads send the same idempotency key at the same instant, many
rounds, once through one shared connection and once with a connection per
thread. A round passes only if exactly one thread believes it created the
payment, nothing raises, and the database ends with one payment and one event.
A round can have several problems at once, so the counts below can overlap.

Part 2: a deterministic replay of one interleaving, the relay's commit
landing between a request's two inserts, showing what the shared connection
makes durable that nobody asked to be.

Run it:  python load_tests/shared_connection_race.py [--rounds 300]
"""

import argparse
import collections
import os
import sys
import tempfile
import threading
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.db import connect, open_connection
from app.payments import create_payment

N_THREADS = 20


def one_round(shared: bool) -> list[str]:
    """Returns every problem seen in one round, empty if the round was clean."""
    path = os.path.join(tempfile.mkdtemp(), "race.db")
    shared_conn = connect(path)
    key = str(uuid.uuid4())
    created_flags, errors = [], []
    lock = threading.Lock()
    start_together = threading.Barrier(N_THREADS)

    def attempt():
        conn = shared_conn if shared else open_connection(path)
        try:
            start_together.wait()
            _, created = create_payment(conn, key, 5000, "usd")
            with lock:
                created_flags.append(created)
        except Exception as e:
            with lock:
                errors.append(f"a request raised {type(e).__name__}: {str(e)[:50]}")
        finally:
            if not shared:
                conn.close()

    threads = [threading.Thread(target=attempt) for _ in range(N_THREADS)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    payments = shared_conn.execute("SELECT COUNT(*) FROM payments").fetchone()[0]
    events = shared_conn.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
    shared_conn.close()

    problems = sorted(set(errors))
    created = sum(created_flags)
    if created > 1:
        problems.append(f"DOUBLE CHARGE: {created} requests each believed they created the payment")
    if payments != 1 or events != 1:
        problems.append(f"database ended with {payments} payments and {events} events")
    return problems


def part1(rounds: int) -> None:
    print(f"Part 1: {N_THREADS} threads, same idempotency key, {rounds} rounds each\n")
    for shared, label in ((True, "one shared connection"), (False, "one connection per thread")):
        per_problem = collections.Counter()
        bad_rounds = 0
        for _ in range(rounds):
            problems = one_round(shared)
            bad_rounds += bool(problems)
            for what in set(problems):
                per_problem[what] += 1
        print(f"  {label}: {bad_rounds} of {rounds} rounds went wrong")
        for what, n in per_problem.most_common():
            print(f"      {n:4d} rounds: {what}")
    print()


def part2() -> None:
    print("Part 2: the relay commits while a request is between its two inserts\n")
    for shared in (True, False):
        path = os.path.join(tempfile.mkdtemp(), "interleave.db")
        request_conn = connect(path)
        relay_conn = request_conn if shared else open_connection(path)
        observer = open_connection(path)  # a third, independent view of what is durable

        # The request has run the first insert of its two statement transaction.
        request_conn.execute(
            "INSERT INTO payments (idempotency_key, amount_cents, currency, status, created_at) "
            "VALUES ('k1', 100, 'usd', 'authorized', 'now')"
        )
        # The relay finishes marking an earlier event dispatched, and commits.
        relay_conn.commit()

        payments = observer.execute("SELECT COUNT(*) FROM payments").fetchone()[0]
        events = observer.execute("SELECT COUNT(*) FROM outbox").fetchone()[0]
        label = "one shared connection" if shared else "separate connections"
        print(f"  {label}: another connection sees {payments} payment and {events} events")
        if payments and not events:
            print("      the payment is committed and the event announcing it does not exist,")
            print("      and if the process died now it never would: the dual write problem")
        for c in {id(request_conn): request_conn, id(observer): observer, id(relay_conn): relay_conn}.values():
            c.close()


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--rounds", type=int, default=300)
    args = ap.parse_args()
    part1(args.rounds)
    part2()
