# Payment Platform (part 1: idempotency and outbox)

The second complete system in this repository, and the first not written in Go. Python and FastAPI here, matching the tech the rest of this portfolio actually uses, not the Java originally sketched in this repository's first planning pass. See [ARCHITECTURE.md](../../ARCHITECTURE.md)'s technology strategy for that decision.

This is a slice, not the whole payment platform described in the repository's original plan: idempotency and the outbox pattern, not the full saga, fraud check, and ledger chain. See Scope below.

## Problem

Payment APIs get called by clients over unreliable networks. A request can time out on the client side after the server already processed it successfully, the client has no way to know which happened, so it does the only safe-seeming thing: it retries. Charging a customer twice because their first request merely looked like it failed is a real, embarrassing, expensive class of bug. Separately, a payment system needs to tell other systems, ledger, notifications, fraud checks, that a payment happened, and that announcement cannot be allowed to go missing just because the network hiccuped on the way to a message broker.

## Production context

Idempotency keys are a documented, publicly used pattern, Stripe's real API uses the exact header name, Idempotency-Key, used here. The transactional outbox pattern is documented in distributed systems literature under that name, and is a common answer to the dual write problem, committing to a database and publishing to a broker as if they were one atomic operation, when they are actually two. The specific numbers in this system's demos are illustrative, not measurements of any real payment processor.

## Requirements

Functional: create a payment exactly once per idempotency key, no matter how many times a request with that key arrives. Guarantee that every committed payment eventually has its event published, even across a crash of the component responsible for publishing.

Non-functional: the common case, a genuinely new payment, should stay fast as the payment table grows. A flood of retries for the same key should cost less than creating a new payment, not the same or more.

## Capacity estimation

At 157.5 microseconds per new payment (see Benchmark), a single SQLite writer handles roughly 6,300 new payments per second before write latency itself becomes the bottleneck, comfortably past what a single process typically needs to sustain before sharding by something else (customer ID, region) becomes the real answer, not a faster single database. Deduplicated retries, 4.7 microseconds each, are close to free by comparison: a retry storm during a network incident degrades gracefully instead of compounding the incident with double charges or a swamped database.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the request sequence, the specific race the idempotency check has to survive, and the outbox relay's crash-and-resume behavior.

## Data model

Two tables, in SQLite: `payments` (idempotency_key as the primary key, amount in integer cents, never floating point, currency, status, created_at) and `outbox` (an autoincrement id, event_type, a JSON payload, created_at, and a nullable dispatched_at that is NULL until the relay publishes it). A partial index on `outbox(dispatched_at) WHERE dispatched_at IS NULL` keeps the relay's polling query cheap regardless of how many already-dispatched rows accumulate.

## API design

```
POST /payments
Header: Idempotency-Key: <string>
Body: {"amount_cents": 5000, "currency": "usd"}

200 OK   {"idempotency_key": "...", "amount_cents": 5000, "currency": "usd",
          "status": "authorized", "created": true or false}

GET /payments/{idempotency_key}
200 OK   the same shape, without "created"
404 Not Found

GET /stats
200 OK   {"total_payments": N, "outbox_pending": N, "outbox_dispatched": N, "published_log_len": N}
```

## Implementation

`app/payments.py` holds both guarantees: `create_payment` does the SELECT-then-INSERT-with-IntegrityError-fallback dance that makes idempotency safe under real concurrency, and `naive_create_payment_no_outbox` exists purely to demonstrate the dual write problem it avoids. `app/outbox_relay.py` is a polling loop, deliberately simple, that publishes undispatched rows and marks them dispatched, with a `fail_after` hook so its crash-and-resume behavior is testable, not just asserted. `app/main.py` is a thin FastAPI layer over both, plus a background thread running the relay continuously. Run it:

```bash
pip install -r requirements.txt
uvicorn app.main:app --reload
```

Then, in another terminal:

```bash
curl -X POST localhost:8000/payments \
  -H "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" \
  -H "Content-Type: application/json" \
  -d '{"amount_cents": 5000, "currency": "usd"}'
```

## Scope

This is part 1 of the payment platform this repository's plan describes. Built: idempotency, the outbox pattern, a relay with proven crash-and-resume behavior. Not built, and clearly not claimed: the saga pattern across multiple services, fraud checks, a full ledger, reconciliation against a real payment processor, and multi-currency handling beyond storing whatever currency string is given. Extending this into that full chain is on this repository's roadmap as later work, not implied to already exist here.

## Scaling

SQLite is a genuine single-writer database, every write in this system serializes through one file, WAL mode lets reads continue concurrently with a write, but there is exactly one writer at a time. That is the honest limit of this implementation, not a detail glossed over: at higher write volume than one process can serialize, the real fix is a client-server database, Postgres being the obvious choice, which removes the single-process constraint but reintroduces everything SQLite sidesteps here, a server process to run, connection pooling, and in this sandbox specifically, a server that cannot reliably survive between separate tool invocations, which is exactly why SQLite was chosen for this environment. The Idempotency-Key uniqueness constraint and the outbox pattern's atomicity requirement both transfer to Postgres unchanged, this system's core logic would not need to change, only db.py's connection layer would.

## Failure modes

Demonstrated, not just described, three of them.

A retry storm, 20 real concurrent HTTP requests with the same key:

```
created=True count: 1 (should be exactly 1)
distinct amounts returned across all 20 responses: {5000} (should be exactly 1 value)
```

The dual write problem, a publish step that fails right after a successful commit:

```
naive: payment committed: True, any outbox event exists to ever announce it: False
outbox: payment committed: True, outbox event exists for it: True
```

The relay crashing mid-batch and resuming: 5 pending events, crash simulated after 3, resumed and picked up exactly the remaining 2, 5 total, none lost, none duplicated, proven in tests/test_outbox_relay.py.

Full numbers and reproduction commands for all three are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

The `/stats` endpoint is the minimal, honest version of what a real deployment would expose properly: total payments, outbox backlog size, and how many events have been dispatched. A full pass, structured logs with a request id, a histogram of relay batch latency, alerting on outbox_pending growing instead of draining, is part of Phase 5 on the repository roadmap.

## Benchmark

Measured on this sandbox, Python 3.12.3, 1 vCPU, Intel Xeon at 2.10GHz:

| Path | per call |
|---|---|
| New payment | 157.5us |
| Deduplicated retry | 4.7us |

Full numbers, the concurrent retry demonstration, and the dual write comparison are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| SELECT-first, INSERT-with-fallback over a lock | The common case, a retry after the original already committed, is one cheap indexed read | Still needs the IntegrityError fallback for the genuine race, the SELECT alone is not sufficient, it is an optimization on top of the real guarantee |
| Outbox pattern over publishing directly after commit | No dual write problem, an event can never be silently lost | An extra table, a relay process to run, and at-least-once rather than exactly-once delivery, which pushes the exactly-once requirement onto the consumer instead, see labs/kafka-messaging |
| SQLite over a client-server database, in this sandbox | Real ACID transactions with no server process to keep alive, which is what made this buildable and testable here at all | A genuine single-writer ceiling that a real high-volume deployment would need to move past, see Scaling |
| Python and FastAPI over Java and Spring Boot | Matches the rest of this portfolio and this developer's actual job search, and was fully buildable and testable in this sandbox with no dependency resolution issues | Java's enterprise transaction-management ecosystem, which is what the repository's original plan chose Java specifically to demonstrate, is not exercised here |

That last row is worth being direct about: choosing Python here was about matching an actual career story, not about which language is technically better suited to transactional systems. Both would have worked. This one is more useful to the person building it.
