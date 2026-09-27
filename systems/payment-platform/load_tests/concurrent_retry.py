"""Simulates the scenario that matters most for a payment API: a client's
request times out on the network, so the client retries with the same
Idempotency-Key, not knowing the original request actually succeeded on
the server. If several retries and the original all arrive close together,
how many actual payments result?
"""

import argparse
import concurrent.futures
import time
import uuid

import httpx


def attempt(base_url: str, key: str, amount_cents: int) -> dict:
    resp = httpx.post(
        f"{base_url}/payments",
        json={"amount_cents": amount_cents, "currency": "usd"},
        headers={"Idempotency-Key": key},
        timeout=5,
    )
    return resp.json()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8000")
    parser.add_argument("--concurrent-clients", type=int, default=20)
    args = parser.parse_args()

    key = str(uuid.uuid4())
    print(f"{args.concurrent_clients} concurrent requests, all with the same Idempotency-Key, simulating a client retry storm after a timeout")

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrent_clients) as pool:
        start = time.perf_counter()
        futures = [pool.submit(attempt, args.url, key, 5000) for _ in range(args.concurrent_clients)]
        results = [f.result() for f in futures]
        elapsed = time.perf_counter() - start

    created_count = sum(1 for r in results if r.get("created"))
    distinct_amounts = {r["amount_cents"] for r in results}

    print(f"elapsed={elapsed*1000:.1f}ms")
    print(f"created=True count: {created_count} (should be exactly 1)")
    print(f"distinct amounts returned across all {len(results)} responses: {distinct_amounts} (should be exactly 1 value)")


if __name__ == "__main__":
    main()
