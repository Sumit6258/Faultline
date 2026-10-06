# Notification Platform

The third complete system in this repository. Multi-channel delivery, a priority and schedule aware queue, retry with backoff, a dead letter queue, and deduplication, composed into one working service rather than demonstrated as separate labs. Go, the same language as the other infrastructure-shaped pieces of this repository.

## Problem

A notification system sits between something that wants to tell a user something, an order shipped, a password reset code, a weekly digest, and the actual channel that reaches them: email, SMS, push. That channel is a third party service with its own rate limits and its own, real chance of being briefly unavailable. A notification platform has to handle a provider failing without losing the notification, handle a caller retrying its own request without sending the message twice, and make sure an urgent password reset doesn't sit behind a backlog of weekly digests.

## Production context

Priority queues, retry with backoff, and dead letter queues are documented patterns used across most production message and notification systems; this repository's own labs/retries-backoff and labs/kafka-messaging cover the backoff formula and the DLQ pattern in isolation. Idempotent submission by client supplied id is the same shape as Stripe's Idempotency-Key, applied here to systems/payment-platform first and to notifications now. The specific numbers in this system's demos, burst capacity 20 for the simulated SMS provider, are illustrative, not a measurement of any real provider's actual limits.

## Requirements

Functional: accept a notification for a channel, deliver it through that channel's provider, retry on failure up to a limit, and give up to a dead letter queue rather than retrying forever. Higher priority notifications should be delivered before lower priority ones once both are due. The same notification id submitted twice should result in one delivery, not two.

Non-functional: many workers need to pull from the same queue safely and without double-delivering anything. A notification scheduled for the future must not block one that's already due, regardless of their relative priority.

## Capacity estimation

At 138ns per push-and-pop (see Benchmark), the queue itself handles well over a million operations per second before it's the bottleneck, nowhere close to what a real provider's own rate limit allows through anyway: the simulated SMS provider in this system's demo caps at 5 per second, which is the actual ceiling, not anything this code does. That's realistic: a notification platform's throughput is almost always set by the channel it's delivering through, not by its own internal bookkeeping.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the submit-to-deliver sequence, why the queue is two heaps instead of one, and the retry-to-DLQ flow.

## Data model

Entirely in memory: the queue's two heaps, a map of claimed notification ids, and each provider's recorded sends. Nothing here persists across a restart, which is a real limitation for anything beyond a demo, see Scaling.

## API design

```
POST /notifications
Body: {"id": "...", "channel": "email|sms|push", "recipient": "...", "body": "...", "priority": "low|normal|high|urgent"}
200 OK   {"accepted": true or false}   (false means this id was already submitted)

GET /stats
200 OK   {"delivered": N, "dead_lettered": N, "queued": N}

GET /dlq
200 OK   [{"Notification": {...}, "LastError": "...", "FailedAt": "..."}]
```

## Implementation

`internal/queue` is a schedule gated priority queue, two heaps, covered above. `internal/senders` defines the `Sender` interface and a local, in-memory implementation standing in for a real provider, since no real email, SMS, or push provider is reachable from the sandbox this was built in; a `RateLimited` wrapper applies a per-channel token bucket, a fresh implementation rather than a shared import, in front of any sender. `internal/dedup` is a claim-once tracker keyed by notification id. `internal/worker` is the pool: it pops due notifications, attempts delivery, and either marks them delivered, reschedules with full jitter backoff, or dead letters them once attempts are exhausted. `notification.go` wires all four into `Submit` and `RunWorkers`. Run it:

```bash
go run ./cmd/server
```

Then, in another terminal, submit one:

```bash
curl -X POST localhost:8080/notifications \
  -d '{"id":"demo-1","channel":"email","recipient":"you@example.com","body":"hi","priority":"urgent"}'
```

## Scaling

Everything here is in memory and single process, which means a restart loses the queue, the dedup record, and the DLQ, not acceptable for a real deployment. The fix is the same one systems/payment-platform and systems/url-shortener already name for their own in-memory pieces: a persistent, shared store behind the same interfaces, so the queue and dedup tracker survive a restart and multiple instances can share one backlog instead of each running an isolated one. The provider senders would become real HTTP clients against real provider APIs, each still behind the same `Sender` interface and the same per-channel rate limiter, neither of which would need to change.

## Failure modes

Demonstrated, not just described, four of them.

Deduplication under real concurrent HTTP requests:

```
20 concurrent submissions, same id: accepted=1 (should be 1)
```

Per-channel rate limiting absorbing a burst past provider capacity:

```
30 SMS sent fast: delivered=28 dead_lettered=2
```

An unroutable channel going straight to the DLQ instead of wasting retries:

```
pigeon-demo in DLQ after 0 attempts: no sender registered for channel carrier-pigeon
```

Priority ordering holding under a scrambled submission order:

```
submitted: low, normal, low, urgent, normal, high, low, urgent, normal
delivered: urgent, urgent, high, normal, normal, normal, low, low, low
```

Full numbers and reproduction commands for all four are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

`/stats` and `/dlq` are the minimal, honest version of what a real deployment needs properly: delivered and dead-lettered counts, current backlog size, and the actual dead letters with their last error. A full pass, per-channel delivery latency, a metric for how often the rate limiter is actually the thing blocking a send versus a genuine provider failure, is part of Phase 5 on the repository roadmap.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Queue Push + Pop | 138.1 | 1 |
| Dedup claim, new id | 456.0 | 1 |
| Dedup claim, duplicate id | 19.5 | 0 |

Full numbers and all four failure mode demonstrations are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Two heaps (scheduled and ready) over one | Correct priority ordering among due items without a future high priority item blocking a due low priority one | More code than a single heap, and two invariants to keep straight instead of one |
| Per-channel rate limiting in front of the sender | Respects a provider's real limit before it would reject the call itself | Rate-limited sends count as failures and consume retry budget, same as a genuine provider error, there's no separate "try again soon, nothing's actually wrong" signal |
| In-memory dedup and queue | Simple, fast, nothing external to run | Everything is lost on restart, and only one process can see one backlog, see Scaling |
| Local senders standing in for real providers | Fully testable and demonstrable without a real email, SMS, or push account | Nothing here has actually sent a real message; the interface is real, the network call behind it is not |

The rate limiting trade-off is worth sitting with: this system can't currently distinguish "the provider is actually down" from "I'm sending faster than the provider allows," both come back as a failed attempt and both consume the same retry budget. A real deployment would want that distinction, since the right response is different, backing off harder for a real outage versus just pacing sends for a rate limit, which is a reasonable next slice rather than something this version gets wrong so much as doesn't yet attempt.
