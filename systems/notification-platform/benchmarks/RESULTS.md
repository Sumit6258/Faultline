# Benchmark results

Measured, not estimated. Reproduce with `go test -bench=. -benchmem ./internal/queue/... ./internal/dedup/...` from this directory, `go run ./load-tests/priority-demo`, and `go run ./load-tests` against a running server.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-10-04

## Core operation cost

| Benchmark | ns/op | allocs/op |
|---|---|---|
| Queue.Push + Queue.Pop | 138.1 | 1 |
| Tracker.Claim, new id | 456.0 | 1 |
| Tracker.Claim, duplicate id | 19.49 | 0 |

A duplicate claim is about 23x cheaper than claiming a new id, the same shape of result as every other idempotency check in this repository (systems/payment-platform's deduplicated retry is a similar ratio): checking a map is cheap, writing to it costs more.

## Priority ordering

From `go run ./load-tests/priority-demo`, a single worker so the order is unambiguous, 9 notifications submitted in a deliberately scrambled priority order:

```
Submission order:
  low-1, normal-1, low-2, urgent-1, normal-2, high-1, low-3, urgent-2, normal-3

Delivery order:
  urgent-1, urgent-2, high-1, normal-1, normal-2, normal-3, low-1, low-2, low-3
```

Strict priority order on delivery, and same-priority items keep their original submission order (normal-1 before normal-2 before normal-3). This is what the two-heap design in internal/queue exists to guarantee; TestQueue_ReadyLowerPriorityBeatsNotYetDueHigherPriority covers the specific case a single heap would get wrong.

## Deduplication under real concurrent HTTP requests

From `go run ./load-tests` against a running server, 20 concurrent POST requests with the same notification id:

```
20 concurrent submissions, same id: accepted=1 (should be 1)
```

## Per-channel rate limiting

Same run, 30 SMS sent as fast as possible against a provider limiter configured for burst 20, refill 5 per second:

```
delivered=28 dead_lettered=2
```

Not all 30 succeed immediately; the rate limiter queues the overflow back onto the notification queue for retry with backoff, and most of it eventually gets through as tokens refill. A couple exhaust their retry budget within the run's timeout and dead letter instead. The exact split varies run to run depending on timing, 28/2 one run, 29/1 another, both runs shown in this lab's development; the ratio, most delivered, a few needing retries or dead lettering, is the real finding, not the exact count.

## Unroutable channel

Same run, one notification submitted for a channel with no registered sender:

```
pigeon-demo in DLQ after 0 attempts: worker: no sender registered for channel carrier-pigeon
```

Zero attempts, not one: an unroutable notification is detected before ever touching a sender, so it doesn't waste a retry budget pretending delivery might work.
