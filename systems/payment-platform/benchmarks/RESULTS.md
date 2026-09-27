# Benchmark results

Measured, not estimated. Reproduce with `python3 benchmarks/bench_create_payment.py`, `python3 load_tests/concurrent_retry.py` against a running server, and `python3 load_tests/dual_write_demo.py`.

## Environment

- Python 3.12.3
- FastAPI 0.141.1, uvicorn 0.54.0, SQLite (Python's built in sqlite3), WAL mode
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-27

## create_payment cost

| Path | total for 2000 calls | per call |
|---|---|---|
| New payment (insert + outbox write, one transaction) | 315.0ms | 157.5us |
| Deduplicated retry (existing key, SELECT only) | 9.4ms | 4.7us |

A deduplicated retry is about 33x cheaper than an actual new payment, since it never opens a transaction at all, it is a single indexed SELECT. The query plan for that SELECT, from `EXPLAIN QUERY PLAN`:

```
SEARCH payments USING INDEX sqlite_autoindex_payments_1 (idempotency_key=?)
```

An index search, not a table scan, using the index SQLite creates automatically for the PRIMARY KEY. This is why the fast path stays fast even as the payments table grows: cost is O(log n) in table size, not O(n).

## Idempotency under real concurrent HTTP requests

This is the result that matters most, from `python3 load_tests/concurrent_retry.py` against a real running server, not a unit test in isolation:

```
20 concurrent requests, all with the same Idempotency-Key, simulating a client retry storm after a timeout
elapsed=537.9ms
created=True count: 1 (should be exactly 1)
distinct amounts returned across all 20 responses: {5000} (should be exactly 1 value)
```

20 real HTTP requests, sent concurrently from 20 threads, all carrying the same Idempotency-Key. Exactly 1 resulted in `created=true`. All 20 responses, including the 19 that hit the race described in architecture/overview.md, agree on the same amount. Confirmed afterward via `/stats`: 1 payment, 1 outbox event, dispatched.

## The dual write problem, demonstrated directly

From `python3 load_tests/dual_write_demo.py`:

```
Naive: commit the payment, then publish as a separate step that fails.
  payment committed: True
  any outbox event exists to ever announce it: False

Outbox: the payment and its event commit in one transaction.
  payment committed: True
  outbox event exists for it: True
```

Same failure, a publish step that raises, injected on purpose in both cases. The naive path leaves a payment that exists with nothing ever able to announce it happened, a silent, permanent inconsistency that only a manual reconciliation job would ever catch. The outbox path cannot lose the event this way, because the event was never a separate step to begin with.

## Relay crash and resume

From `tests/test_outbox_relay.py::test_crash_mid_batch_then_resume_dispatches_remainder_not_duplicates`: 5 pending events, the relay simulated to crash after dispatching 3, then resumed. Result: exactly 3 dispatched before the crash, exactly 2 more after resuming, 5 total, none lost, none duplicated. The relay's restart behavior costs nothing extra to get right, it falls out directly from dispatch state living in the same table as the events themselves.
