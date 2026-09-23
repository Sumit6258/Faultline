# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=caching` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Command: go test -bench=. -benchmem -run=^$ ./...
- Date: 2026-09-23

## Results

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| LRU.Get, hit | 81.09 | 0 | 0 |
| LRU.Set, existing key | 82.76 | 0 | 0 |

## Reading these numbers

Both operations are a mutex lock, a map lookup, and a linked list move, and both land around 80ns with no allocation. The Set benchmark repeatedly writes the same key, which is the update-in-place path. Setting a brand new key allocates one list element and one map entry, which this benchmark doesn't isolate, a benchmark that inserts N distinct keys per iteration would be needed to measure that path specifically.

## The number that actually matters: source calls, not nanoseconds

The interesting result in this lab isn't a nanosecond figure. It's how many times the simulated database gets hit during a stampede. Run `go run ./cmd/demo`:

```
Cache stampede demo: 100 concurrent requests for the same missing key.

naive, no coalescing:     source hit 100 times, wall clock 30.587449ms
coalesced, cache-aside:   source hit 1 times, wall clock 30.48454ms
```

Wall clock time is close to identical between the two runs here, because the simulated source just sleeps for a fixed 30ms with no connection limit or CPU contention modeled, so 100 concurrent sleeps finish in about the same time as 1. A real database would not behave this way: 100 concurrent connections opening at once for a single hot key is itself the failure, whether or not each individual query is fast, since it can exhaust a connection pool or spike CPU on the database host. The number that actually demonstrates the fix is the source call count: 100 versus 1.
