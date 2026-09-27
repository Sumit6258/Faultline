# URL Shortener

The first complete system in this repository, built from labs and patterns that exist elsewhere in Faultline as standalone lessons: caching, rate limiting, and consistent hashing all show up here in a working system instead of in isolation. See [ARCHITECTURE.md](../../ARCHITECTURE.md) for how systems relate to labs.

## Problem

Turn a long URL into a short code, and turn that code back into the original URL on demand, at whatever volume the redirect endpoint receives. The interesting engineering isn't the mapping itself, a dictionary would do that, it's everything a real deployment needs around it: codes that don't collide, protection against one client flooding the system with junk links, and a redirect path fast enough that a cache stampede or a slow analytics write never becomes the user's problem.

## Production context

Public write-ups from companies operating URL shorteners at scale describe the same shape of system: an ID generation scheme (often counter based, sometimes with a reversible encoding), a cache in front of the datastore because traffic is heavily skewed toward a small number of popular links, and asynchronous click tracking so analytics never sits on the redirect's critical path. The specific numbers in this system's demos, 1000 URLs, a burst limit of 20, are illustrative, not measurements of any real company's deployment.

## Requirements

Functional: create a short code for a URL, resolve a code back to its URL, count clicks per code.

Non-functional: code generation must not collide. A redirect, the path a real user is waiting on, must stay fast regardless of analytics or cache state. Creation must be protected against abuse from a single client.

## Capacity estimation

A service seeing 1000 URL creations per second and 100,000 redirects per second, a realistic ratio given how much more often a link is clicked than created, spends most of its request volume on reads. At roughly 55ns per cache-hit resolve (see Benchmark below), that's about 5.5 milliseconds of CPU per second on cache hits alone, negligible. The real capacity question is cache sizing: this system's own load test shows a cache holding the actual working set (962 distinct hot codes) hitting 95.2% of requests, while a cache an order of magnitude smaller drops to 38.5%, see Failure Modes. Sizing the cache to the working set, not to the total number of URLs ever created, is the number that actually matters.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the request flow, the full V0 to V7 evolution this system is working toward, and two real bugs the load test itself surfaced during development.

## Data model

In memory only, in this version: a single `map[string]string` behind a mutex, see internal/store. The Store interface is written so a persistent, real backing store is a drop-in replacement, nothing above it would need to change, see Scaling.

## API design

```
POST /shorten
Body: {"url": "https://example.com/a/very/long/path"}
Header: X-Client-ID: <string>, optional, defaults to the caller's IP

200 OK                    {"code": "7"}
429 Too Many Requests     (rate limited)

GET /{code}
302 Found, Location: the original URL
404 Not Found             (unknown code)

GET /stats
200 OK                    {"CacheHits": N, "CacheMisses": N}
```

## Implementation

Five small, independently tested pieces, wired together by `Service` in shortener.go: `idgen` (two strategies, compared below), `store` (in-memory, an interface so a persistent version can drop in later), `cache` (a fixed-capacity LRU), `ratelimit` (a per-client token bucket for abuse prevention), and `analytics` (an asynchronous, non-blocking click counter). `Service.Shorten` and `Service.Resolve` are the only two operations that matter; everything else supports one of those two paths. Run it directly:

```bash
go run ./cmd/server
```

Then, in another terminal:

```bash
curl -X POST localhost:8080/shorten -d '{"url":"https://example.com"}'
curl -L localhost:8080/<the returned code>
```

## Scaling

This system currently sits at V0 through V2 of its own evolution, see architecture/overview.md: single process, cached, abuse-guarded, asynchronous where it counts. V3, a persistent store, was attempted for real, Postgres runs fine in this sandbox, before being set aside because a running server process doesn't reliably survive across separate tool invocations here, which is not a constraint a real deployment has. The Store interface is deliberately shaped so that swap is mechanical: implement Save, Load, and Exists against Postgres, and nothing in Service changes. Past that, V4 (multiple stateless app servers behind a load balancer) needs the cache to move from in-process to shared, Redis is the usual choice, at the cost of a network hop per lookup instead of an in-process map read, exactly the trade-off measured in labs/caching. V6, sharding the store, is where labs/consistent-hashing's measured remapping-cost numbers become directly relevant to this system, not just an interesting lab on their own.

## Failure modes

Demonstrated, not just described, three of them.

A too-small cache under realistic hot-URL traffic:

```
cache-size=1000: hit ratio=95.2%
cache-size=20:   hit ratio=38.5%
```

One client attempting to flood creation:

```
40 requests as fast as possible, burst capacity 20, refill 5/sec
allowed=20 blocked=20
```

Random ID collisions rising as the keyspace fills, from labs/idgen's own tests:

```
average attempts per successful generation over 3000 codes in a 3844 code space: 1.97
```

Full numbers and methodology for all three are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md), which also documents two real bugs the load test itself caught during development, worth reading for what they were, not just that they were fixed.

## Observability

The `/stats` endpoint exposes cache hit and miss counts directly, which is what the load test actually reads to measure cache effectiveness, rather than inferring it from timing. A full observability pass, structured logs, traces, Prometheus metrics, is part of Phase 5 on the repository roadmap; `/stats` is a minimal, honest stand-in for now, not a finished solution.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Shorten | 827.1 | 7 |
| Resolve, cache hit | 55.6 | 0 |
| Resolve, cache miss | 588.2 | 2 |

Full numbers, the cache load test, and the abuse-prevention demonstration are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Counter-based ID generation | Never collides, cheapest possible generation | Codes are sequential and guessable, anyone can enumerate every code ever issued |
| Random ID generation, the alternative implemented here | Not guessable in sequence | A real, measured, growing collision rate as the keyspace fills, and a check-then-write race unless the store's Save is itself atomic on the code |
| Cache-aside over write-through | Simple, the cache only ever holds what's actually been requested | First request for any code always pays full store latency |
| Dropping analytics events under load instead of blocking | The redirect path never slows down because of analytics | Click counts become approximate under sustained overload, an explicit, deliberate trade, not an oversight |

The recurring theme across all four rows is the same one from labs/caching and labs/rate-limiting: every one of these trades write-side or bookkeeping cost for read-side speed, or the other way around, and which side of that trade a URL shortener should take depends entirely on which side, creation or redirect, actually dominates its real traffic. For this system, redirects dominate by a wide margin, which is why every design choice above favors the read path.
