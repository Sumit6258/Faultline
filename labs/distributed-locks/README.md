# Distributed Locks

## Problem

Two workers should never both process the same job at once, but in a distributed system, "only one worker holds the lock" isn't actually enough of a guarantee on its own. A worker can stall, a long garbage collection pause, a slow network, a descheduled thread, past the point its lock should have been taken away, and then wake up still believing it holds the lock. If nothing stops it, it can write to shared state at the same time as whoever took over. This lab is less about acquiring a lock, which is the easy part, and more about that specific failure.

## Production context

Time bounded leases and fencing tokens are a documented pattern, described in Martin Kleppmann's widely referenced writing on distributed locking, and used in production lock services including those built on ZooKeeper, etcd, and Redis based approaches like Redlock. The specific numbers in this lab's demo are illustrative, not measurements of any of those systems.

## Requirements

Functional: grant a lease on a named resource to one holder at a time, let the holder renew or release it, and let another holder take over once a lease expires. Give every successful acquire a token higher than any token issued before.

Non-functional: acquiring a lock has to be safe under concurrent attempts, exactly one caller should win a contested acquire, never zero and never more than one.

## Capacity estimation

Each held lease costs one map entry, holder name, token, and an expiry timestamp, well under 100 bytes. A service managing 100,000 concurrent leases costs single digit megabytes of memory in the lock service itself. The real constraint in production is usually availability of the lock service, not its memory footprint: if it's a single process with no replication, it's a single point of failure for everything that depends on it.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the sequence of events in the stale write scenario, with and without fencing.

## Data model

In memory only in this lab: a map from resource name to lease state (holder, token, expiry), guarded by a mutex, plus a single monotonic counter for issuing tokens. A production lock service would typically back this with a replicated store so the lock state survives the lock service's own process dying, which this lab doesn't build.

## API design

A Go library, not an HTTP API:

```go
svc := distlock.NewService()
token, ok := svc.TryAcquire("job-1", "worker-A", 30*time.Second)
svc.Renew("job-1", "worker-A", token, 30*time.Second)
svc.Release("job-1", "worker-A", token)
```

## Implementation

`Service` grants leases and issues fencing tokens. `FencedResource` stands in for whatever the lock is protecting, and rejects any write carrying a token lower than the highest one it has already seen. `NaiveResource` is the same idea with no fencing check at all, built only so its failure can be measured against `FencedResource` rather than just described. Run the side by side comparison directly:

```bash
go run ./cmd/demo
```

## Scaling

A single lock service instance is a single point of failure: if it goes down, nothing depending on it can acquire or release a lock until it's back. Production lock services solve this with their own replication and consensus underneath, which is exactly what etcd and ZooKeeper are for, so the lock service itself doesn't become the thing that takes the whole system down. That's out of scope for this lab specifically, and it's a large part of why the Consensus lab exists later on this repository's roadmap.

## Failure modes

Demonstrated, not just described. Worker A acquires a lease, stalls past it, worker B takes over and writes, then A wakes up and tries to write with its now stale token:

```
With fencing tokens:
  worker-A's write accepted=false
  resource's final value: "written by B"

Without fencing, same scenario:
  resource's final value: "written by A, but stale"
```

Same sequence of events in both cases. The only difference is whether the resource checks a token. Full output and the reasoning behind it are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metrics that would matter most are `lock_acquire_total{result="granted|denied"}`, `lock_active_leases`, and specifically `lock_fencing_rejections_total`, since a nonzero value there means the system is actually catching a stale writer in production, which is worth alerting on, not just logging.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Service.TryAcquire, new resource | 1020 | 3 |
| FencedResource.Write | 16.3 | 0 |

Full numbers and the fencing demonstration output are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Time bounded leases over permanent locks | A crashed holder can't block the resource forever | The holder has to actively renew, and needs sensible handling for what happens if renewal itself fails |
| Fencing tokens over trusting "am I still the holder" | Protects the resource even when the lock service and the holder disagree about who owns the lock | Every protected resource has to be changed to check a token, the lock service alone isn't enough |
| Single lock service over a replicated one | Simple to build and reason about, which is why this lab does it this way | A single point of failure, and exactly the problem real lock services like etcd exist to solve |

The core lesson of this lab isn't really about locks. It's that "I hold the lock" is a belief a client has about the world, and beliefs can be wrong for a while before the client finds out. Fencing tokens don't stop that belief from being wrong. They stop it from mattering.
