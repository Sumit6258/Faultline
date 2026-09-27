# Benchmark results

Measured, not estimated. Reproduce with `go test -bench=. -benchmem ./...` from this directory, or `go run ./load-tests` against a running server for the load test numbers below.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-26

## Per-operation cost

| Operation | ns/op | B/op | allocs/op |
|---|---|---|---|
| Shorten | 827.1 | 175 | 7 |
| Resolve, cache hit | 55.6 | 0 | 0 |
| Resolve, cache miss | 588.2 | 108 | 2 |

A cache hit is about 10.6x cheaper than a miss. Shorten costs the most, since it touches the rate limiter, the ID generator, and the store, in that order, all before returning.

## ID generation and collisions

From `go test -v ./internal/idgen`, filling a deliberately small 2 character code space, 3844 possible codes, past 78% occupancy:

```
average attempts per successful generation over 3000 codes in a 3844 code space: 1.97
```

Counter based generation never collides at any occupancy, by construction. Random generation's average attempt count climbing toward 2 well before the space is even half exhausted is the birthday paradox showing up directly: this is exactly why real systems either use a counter-like scheme or size the random keyspace with real headroom past 50% occupancy, not right up to it.

## Cache effectiveness under realistic hot-URL traffic

This is the result that matters most, and it comes from `go run ./load-tests` against a real running server, not a benchmark. 1000 short URLs created, then 20000 redirect requests drawn from a Zipfian distribution, math/rand's own model for exactly this: a small number of codes dominate total traffic, which is what real hot-URL access looks like.

```
cache-size=1000 (large enough for the working set):
  20000 requests against 962 distinct codes touched
  cache hits=19038 misses=962 hit ratio=95.2%

cache-size=20 (far smaller than the working set):
  same 962 distinct codes touched
  cache hits=7690 misses=12310 hit ratio=38.5%
```

Same traffic pattern, same 962 codes actually in play, the only difference is cache capacity. A cache sized to hold the real working set turns nearly all traffic into sub-100ns lookups. A cache that's too small for that working set spends most of its time evicting entries it's about to need again, dropping to 38.5%, worse than a coin flip.

## Abuse prevention

One client sends 40 creation requests as fast as possible against the default limiter, burst capacity 20, refill 5 per second:

```
allowed=20 blocked=20
```

Exactly the configured burst capacity, no more, no less. This result took two real bugs to get right during development, worth naming since both are the kind of mistake that's easy to ship: first, the load test's own bulk setup was silently rate limited too, masking the cache comparison above until the setup client was given a separate, higher limit. Second, the server originally kept per-client state using the connection's full RemoteAddr, which includes an ephemeral port that changes on every new connection, so every request looked like a different client and nothing was ever actually rate limited. The fix was keying on the IP alone. Both bugs were caught by actually running the demonstration and getting a suspicious number, not by reasoning about the code.
