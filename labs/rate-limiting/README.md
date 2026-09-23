# Rate Limiting

## Problem

A service with no rate limiting has no way to stop one client, misbehaving or malicious, from consuming all of its capacity and starving every other client. Rate limiting exists to enforce a budget: at most N requests per client per unit of time.

## Production context

Fixed window, sliding window, token bucket, and leaky bucket are all documented, widely used strategies. They show up under these names in most API gateway and CDN documentation, Cloudflare and Kong among them. The specific limits used in this lab's demos are illustrative, not pulled from any company's real production configuration.

## Requirements

Functional: given a client identifier and a configured limit, decide whether to allow or reject a request, in roughly constant time so the limiter itself never becomes the bottleneck.

Non-functional: the check has to be safe under concurrent access from many goroutines at once, and its own overhead has to be small relative to the request it's guarding, single digit microseconds at most.

## Capacity estimation

A service handling 10,000 requests per second, checking the limiter once per request at roughly 100ns per check (see Benchmark below), spends about 1 millisecond of CPU time per second on rate limiting. That's noise. The real cost at scale isn't CPU, it's memory: one map entry per active client. At 1 million active clients and roughly 50 bytes per entry, that's about 50MB, which is why the "never evicts idle keys" limitation noted in fixed_window.go matters once this runs for real.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the request flow and the distributed failure mode, with diagrams.

## Data model

In memory only in this lab: a map of client ID to state, one per algorithm, guarded by a mutex. No database. The Redis backed variant described under Scaling would use one key per client (ratelimit:{clientID}) with a TTL.

## API design

The demo server exposes one endpoint:

```
GET /
Header: X-Client-ID: <string>

200 OK                    {"status":"ok"}
429 Too Many Requests     {"error":"rate limit exceeded"}, Retry-After: 1
```

If no X-Client-ID header is sent, the client's remote address is used instead.

## Implementation

Four algorithms, one shared interface:

```go
type Limiter interface {
    Allow(key string) bool
}
```

FixedWindow, SlidingWindowCounter, TokenBucket, and LeakyBucket each implement it. All four take an injected clock so the tests don't depend on real sleeps. Run any of them behind the demo HTTP server:

```bash
go run ./cmd/server -algo=token-bucket -limit=10
```

## Scaling

At one process, any of these four algorithms works as written. The moment there's more than one process behind a load balancer, in memory state stops being correct. See Failure Modes below and the measured numbers in load-tests/RESULTS.md. The fix is moving the counter into a shared store, typically Redis, with an atomic increment and check. That adds a network round trip to every request and makes Redis a hard dependency: if it's down, the service has to decide whether to fail open (allow everything) or fail closed (reject everything), which is its own real design decision with real consequences. This variant isn't built yet. It's on the repository roadmap.

## Failure modes

Demonstrated, not just described. Three independent server instances, each configured with limit=10 and no shared state, stand in for three nodes behind a load balancer:

```
30 requests from one client, round robined across 3 independent nodes, limit=10 each:
allowed=30 rejected=0

the same 30 requests against a single node with the same limit=10:
allowed=10 rejected=20
```

Full setup and reproduction steps are in [load-tests/RESULTS.md](load-tests/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metrics this would expose are ratelimit_requests_total{result="allowed|rejected"} and ratelimit_active_keys, following the naming convention that will live in docs/observability/conventions.md once the shared observability kit exists.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz, via go test -bench=. -benchmem ./...:

| Algorithm | ns/op | allocs/op |
|---|---|---|
| FixedWindow | 87.9 | 0 |
| SlidingWindowCounter | 101.4 | 0 |
| TokenBucket | 95.4 | 0 |
| LeakyBucket | 103.0 | 0 |
| TokenBucket, 10000 distinct keys | 167.5 | 1 |

Full numbers, environment details, and the load test results are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md) and [load-tests/RESULTS.md](load-tests/RESULTS.md).

## Trade-offs

| Algorithm | Allows bursts | Memory per key | Boundary problem |
|---|---|---|---|
| Fixed window | No, capped at the limit per window | One counter | Yes, up to 2x the limit right at a window edge |
| Sliding window counter | Slightly | One counter plus one previous count | Mostly fixed, it's an approximation |
| Token bucket | Yes, up to capacity | One float plus a timestamp | None |
| Leaky bucket | No, smooths into a steady output | One float plus a timestamp | None, though this implementation rejects excess requests rather than queueing them |

Token bucket is the most common default because it allows reasonable bursts (a user opening five tabs at once shouldn't get rate limited) while still enforcing a real ceiling. Fixed window is the cheapest to reason about and implement correctly, which is why it still shows up often despite the boundary problem. Leaky bucket fits best when the downstream system genuinely needs a smoothed, steady rate, like a queue worker that can only process at a fixed pace.
