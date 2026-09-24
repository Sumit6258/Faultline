# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=distributed-locks` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-23

## Get/Write throughput

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| Service.TryAcquire, new resource each call | 1020 | 195 | 3 |
| FencedResource.Write | 16.34 | 0 | 0 |

TryAcquire costs more than a simple map write because each call in this benchmark uses a brand new resource name, string formatting for the key plus a map insert into a growing map. Reacquiring the same resource repeatedly would be cheaper, closer to FencedResource.Write's cost, since it's an update rather than an insert. FencedResource.Write itself is about as cheap as this kind of code gets: a mutex lock and an integer comparison.

## The fencing demonstration

This is the number that actually matters in this lab, and it comes from `go run ./cmd/demo`, not a benchmark, since it's a correctness property, not a speed measurement:

```
With fencing tokens:
  worker-A acquires the lease, token=1
  worker-A stalls for 300ms, past its 200ms lease...
  worker-B acquires the lease after expiry, ok=true token=2
  worker-B writes to the resource
  worker-A wakes up, unaware it lost the lease, tries to write with its stale token
  worker-A's write accepted=false
  resource's final value: "written by B"

Without fencing, same scenario:
  resource's final value: "written by A, but stale"
```

Same sequence of events, same stalled worker, same late write. The only difference is whether the resource checks a fencing token. With it, the stale write is rejected and the data stays correct. Without it, the stale write silently wins and the data is wrong, with nothing in the system flagging that anything went awry.
