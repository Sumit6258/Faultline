# Caching

## Problem

Reading from a database or any slow upstream on every request doesn't scale: the upstream becomes the bottleneck long before the rest of the service does. A cache in front of it absorbs repeat reads. But a cache introduces its own failure mode: if a popular key expires and a burst of requests arrive before any of them repopulates it, all of them miss at once and hit the upstream simultaneously, which is exactly the load spike the cache was supposed to prevent.

## Production context

Cache-aside is one of the most widely documented caching patterns, described in most caching and CDN vendor documentation under that name or "lazy loading." Request coalescing under load (sometimes called singleflight, following the name of Go's own library for it) is a documented technique for preventing the specific failure described above, generally called a cache stampede or thundering herd. This lab implements coalescing from scratch rather than importing a library, since the point is to understand the mechanism.

## Requirements

Functional: serve a value from cache when present and not expired, and fall back to the upstream on a miss, populating the cache for next time.

Non-functional: concurrent misses for the same key must not each independently hit the upstream. The cache itself must be safe under concurrent access and bounded in size, since an unbounded cache is just a memory leak with extra steps.

## Capacity estimation

For a cache holding 100,000 entries at roughly 200 bytes each (a short key plus a small value plus the LRU bookkeeping), that's about 20MB, comfortably within a single process. The number that matters more than memory is the hit ratio: at a 95% hit ratio, an upstream doing 10,000 requests per second without a cache only sees 500 requests per second with one, which is usually the entire point of adding a cache in the first place.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the read path and the stampede failure mode, with diagrams.

## Data model

In memory only in this lab: a map plus a doubly linked list for LRU ordering, and a second map tracking in flight calls for coalescing. No external cache is used here. A production deployment would typically put this same cache-aside logic in front of Redis or Memcached rather than an in-process map, so the cache is shared across replicas instead of each process having its own.

## API design

Not an HTTP API in this lab, a Go interface used as a library:

```go
type CacheAsideStore struct { /* ... */ }

func (c *CacheAsideStore) Get(key string) (string, bool)
```

## Implementation

Four pieces: `SlowStore` simulates the upstream and counts how many times it was actually called, so tests can prove behavior instead of asserting on timing. `LRU` is a fixed capacity, TTL aware cache built on `container/list`. `Coalescer` is a from-scratch implementation of the singleflight pattern: concurrent callers for the same key share one in-flight call instead of each starting their own. `CacheAsideStore` wires the three together.

Run the stampede comparison directly:

```bash
go run ./cmd/demo
```

## Scaling

At one process, an in-memory cache works as written, but it isn't shared: two replicas of the same service each have their own cache, each with their own miss the first time a key is requested, and no way to invalidate a key in one process when it changes in another. Moving the cache into Redis solves the sharing problem, at the cost of a network round trip per lookup instead of an in-process map read, and Redis becoming a dependency the service needs to handle being unavailable. Coalescing gets harder too: an in-process mutex only coordinates goroutines in one process, so distributed coalescing needs something like a Redis lock or the SETNX pattern to make sure only one replica, not just one goroutine, fetches on a miss.

## Failure modes

Demonstrated, not just described. A cache stampede: 100 concurrent requests for the same missing key.

```
naive, no coalescing:     source hit 100 times, wall clock 30.6ms
coalesced, cache-aside:   source hit 1 times, wall clock 30.5ms
```

The wall clock times are close because this lab's simulated upstream just sleeps for a fixed duration with no connection limit modeled. A real database would not shrug off 100 simultaneous connections for one key the way this simulation does. The number that matters is the call count: 100 versus 1. Full details in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metrics this would expose are `cache_requests_total{result="hit|miss"}`, `cache_evictions_total`, and `cache_coalesced_calls_total`, following the naming convention that will live in docs/observability/conventions.md once the shared observability kit exists.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Get, cache hit | 81.1 | 0 |
| Set, existing key | 82.8 | 0 |

Full numbers and the stampede demo output are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Cache-aside over write-through | Simpler, cache only holds what's actually been read | First read after expiry always pays the full upstream latency |
| LRU eviction over random or FIFO | Keeps the actually-hot keys in cache under memory pressure | Bookkeeping cost per access, a doubly linked list touch on every Get |
| Coalescing over letting every miss through | Caps upstream load at 1 concurrent fetch per key regardless of request volume | All waiters share one fetch's latency, none of them can get a faster or fresher answer by trying again |
| In-process cache over shared Redis | No network hop, sub-microsecond access | Not shared across replicas, and each replica's cache is cold on startup |
