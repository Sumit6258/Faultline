"""A plain timing benchmark, not a testing framework fixture: how much does
create_payment itself cost, separate from any HTTP overhead. Run directly:
python3 benchmarks/bench_create_payment.py

With --format go it prints lines in the format `go test -bench` uses, which is
what bench/ (faultbench) reads to include this in the repository-wide report.
"""

import argparse
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.db import connect
from app.payments import create_payment


def bench_new_payments(n: int) -> float:
    conn = connect("/tmp/bench_new.db")
    conn.execute("DELETE FROM payments")
    conn.execute("DELETE FROM outbox")
    conn.commit()

    start = time.perf_counter()
    for _ in range(n):
        create_payment(conn, str(uuid.uuid4()), 5000, "usd")
    elapsed = time.perf_counter() - start
    conn.close()
    return elapsed


def bench_duplicate_key(n: int) -> float:
    conn = connect("/tmp/bench_dup.db")
    conn.execute("DELETE FROM payments")
    conn.execute("DELETE FROM outbox")
    conn.commit()

    key = str(uuid.uuid4())
    create_payment(conn, key, 5000, "usd")  # the real one

    start = time.perf_counter()
    for _ in range(n):
        create_payment(conn, key, 5000, "usd")  # every one of these is a deduplicated retry
    elapsed = time.perf_counter() - start
    conn.close()
    return elapsed


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--format", choices=["text", "go"], default="text", help="'go' prints lines faultbench can read")
    ap.add_argument("-n", type=int, default=2000, help="operations per benchmark")
    args = ap.parse_args()
    n = args.n
    new_elapsed = bench_new_payments(n)
    dup_elapsed = bench_duplicate_key(n)

    if args.format == "go":
        print(f"BenchmarkCreatePaymentNew {n} {new_elapsed / n * 1e9:.1f} ns/op")
        print(f"BenchmarkCreatePaymentDeduplicatedRetry {n} {dup_elapsed / n * 1e9:.1f} ns/op")
        return

    print(f"create_payment, {n} new payments: {new_elapsed*1000:.1f}ms total, {new_elapsed/n*1e6:.1f}us/op")
    print(f"create_payment, {n} deduplicated retries of one key: {dup_elapsed*1000:.1f}ms total, {dup_elapsed/n*1e6:.1f}us/op")


if __name__ == "__main__":
    main()
