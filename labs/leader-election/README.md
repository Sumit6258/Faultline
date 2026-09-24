# Leader Election

## Problem

Some jobs must have exactly one owner at a time: a scheduler deciding when to run a cron job, a coordinator assigning work to a pool of workers, anything where two nodes doing it simultaneously causes duplicate work or worse. Leader election is how a group of otherwise equal nodes agrees on which one of them is in charge right now, and, just as importantly, notices when that node has died and picks a replacement.

## Production context

Heartbeat based leader election with a timeout is a documented, widely used pattern, the same underlying idea behind leader election in etcd, Kubernetes controller leader election, and many custom schedulers. Real systems usually build this on top of a consensus protocol like Raft, so that the "who is leader" state itself survives a node crashing, which this lab's single shared Cluster does not attempt to do. See Scaling below.

## Requirements

Functional: any node can attempt to become leader. Exactly one node should hold leadership at a time. If the leader stops heartbeating, another node should be able to take over after a bounded amount of time.

Non-functional: the decision of who is leader has to be safe under concurrent attempts, exactly one winner, never zero, never two. Detecting a dead leader should happen within a known, boundable amount of time, the timeout.

## Capacity estimation

The cluster holds a handful of fields, a leader name, a term number, a timestamp, regardless of how many nodes are heartbeating against it. The real capacity question isn't memory, it's heartbeat frequency versus timeout: nodes need to heartbeat often enough, relative to the timeout, that normal network jitter doesn't look like a crash. A common starting point is a timeout at least 3 to 5 times the heartbeat interval.

## Architecture

See [architecture/overview.md](architecture/overview.md) for normal operation and what an election looks like once the leader goes silent.

## Data model

In memory only in this lab: the current leader's name, a term counter, and the timestamp of its last heartbeat, guarded by a mutex. No persistence. If this process restarts, all memory of who was leader is gone, which is fine for this lab and not fine for a real system, see Scaling.

## API design

A Go library, not an HTTP API:

```go
c := leaderelection.NewCluster(5 * time.Second)
term, isLeader := c.Heartbeat("node-1")
```

Every node calls Heartbeat on a timer. The return value tells it whether it's currently the leader.

## Implementation

`Cluster` holds the shared state: current leader, current term, and when the leader last checked in. `Heartbeat` is the only real operation, it's how a node claims leadership, renews it, or finds out someone else already has it. Term increases whenever leadership changes hands, including when the same node reclaims leadership after its own heartbeat arrived late, since the cluster has no way to know whether someone else took over in that gap. Run the full scenario, a leader that heartbeats normally, then crashes, then a new election:

```bash
go run ./cmd/demo
```

## Scaling

This lab's Cluster is one shared piece of state that every node calls into directly, which is exactly why the race resolves cleanly: there's only one copy of the truth to check against. A real distributed system doesn't have that luxury, there's no single trusted process every node can safely call to ask "who's the leader," because that process would itself be a single point of failure. Production leader election runs on top of a consensus protocol, so that the leadership state is replicated and survives any single node, including the one that was just leader, going down. That's genuinely harder than this lab, and it's why Consensus (Raft) is its own lab later on this repository's roadmap rather than an extension of this one.

## Failure modes

Demonstrated, not just described. node-1 leads normally, then crashes:

```
node-1 heartbeat: term=1 leader=true
node-1 heartbeat: term=1 leader=true   (x3, normal renewals)
node-1 crashes. it stops heartbeating.
waiting past the 200ms timeout...
node-2 heartbeat: term=2 leader=true
node-3 heartbeat: term=2 leader=false
final leader: node-2, term 2
```

Exactly one of node-2 and node-3 won the new term, decided by whichever heartbeat the cluster processed first. Full output is in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metric that matters most is `leader_elections_total`, a counter that increments on every term change. A production system watching this metric would alert if it climbs too fast, since frequent re-elections usually mean the timeout is tuned too aggressively for the network it's running on, not that nodes are actually crashing that often.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Heartbeat, same leader renewing | 71.78 | 0 |

Full numbers and the election demo output are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Heartbeat timeout over instant failure detection | Tolerates brief network hiccups without triggering a false failover | A real crash isn't noticed until the timeout elapses, that's dead time with no leader |
| Short timeout | Fast failover after a real crash | More prone to false failovers during transient network trouble |
| Long timeout | More tolerant of jitter, fewer false failovers | Longer window with no leader after a genuine crash |
| Single shared Cluster, this lab | Simple, the race resolves cleanly | Not how a real distributed system can work, there's no single trusted process to call, see Scaling |

Choosing a timeout is really a bet about your network, not a fact about your system. This lab hardcodes 200ms in the demo because that's convenient to watch happen in under a second. A real deployment would set it based on measured heartbeat latency and jitter, not a round number.
