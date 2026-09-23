# Consistent Hashing

## Problem

Any system that shards data or requests across multiple nodes, a cache, a database, a set of workers, needs a way to decide which node owns which key. The obvious approach, hash(key) mod N, works fine right up until N changes. The moment a node is added or removed, the modulus changes, and nearly every key maps to a different node than before, even though most nodes never went anywhere. For a cache, that means a near total wipeout of hit rate the instant you scale. For a sharded database, it means moving almost all the data. Consistent hashing exists to make N changing a small event instead of a full reshuffle.

## Production context

Consistent hashing is documented, publicly used infrastructure: it's the basis of Amazon's Dynamo paper's partitioning scheme, and it shows up in Cassandra's and DynamoDB's partitioning, in client side sharding for Memcached, and in various load balancer implementations. The specific numbers in this lab's demo (5 nodes, 10000 keys) are illustrative, not measurements of any of those systems.

## Requirements

Functional: given a key, deterministically return the same node every time, as long as the set of nodes hasn't changed. When a node is added or removed, minimize how many keys change which node they map to.

Non-functional: lookups need to be fast enough to sit on a hot path, and the structure needs to be safe under concurrent reads while nodes are rarely added or removed.

## Capacity estimation

With 20 real nodes and 150 virtual replicas per node, the ring holds 3000 sorted hash values, 4 bytes each plus map overhead for the hash to node lookup, well under a megabyte total. That scales fine into the low thousands of real nodes before the ring itself becomes a memory concern. The real capacity question is virtual node count: more replicas smooth out the distribution (see Benchmark below) at the cost of more memory and a slower binary search per lookup.

## Architecture

See [architecture/overview.md](architecture/overview.md) for how a lookup works and why removing a node only moves that node's own keys.

## Data model

No external storage. A sorted slice of `uint32` hash values, a map from hash value to node name, and a set of node names, all held in memory and guarded by a mutex. In a real deployment this ring lives in every client that needs to route requests, kept in sync as nodes join or leave, rather than being a separate service.

## API design

A Go library, not an HTTP API:

```go
r := consistenthash.NewRing(150)
r.AddNode("cache-1")
r.AddNode("cache-2")
node := r.Get("user:12345") // which node owns this key
r.RemoveNode("cache-1")
```

## Implementation

`Ring` hashes each real node `replicas` times using `hash/crc32` from the standard library, no external dependency, and keeps every resulting hash value in one sorted slice. `Get` hashes the requested key once and binary searches for the next node clockwise. `NaiveModN` implements the obvious alternative, hash mod len(nodes), purely so its weaknesses can be measured against `Ring` rather than just asserted. Run the comparison directly:

```bash
go run ./cmd/demo
```

## Scaling

The ring itself scales to a large number of virtual points without much trouble, the cost is a binary search over a sorted slice, O(log(nodes times replicas)). The part that actually gets harder at scale is keeping every client's view of the ring consistent: if two clients disagree about which nodes are currently in the ring, they'll route the same key to different nodes. Real systems solve this with a coordination service (ZooKeeper, etcd, or similar) that all clients watch for ring membership changes, which this lab doesn't build. It's a reasonable candidate for the Leader Election or Consensus labs later on this repository's roadmap.

## Failure modes

Demonstrated, not just described. Two things break when the node set changes.

Remapping, removing 1 of 5 nodes, 10000 keys:

```
naive mod-N:        7945 of 10000 keys moved (79.5%)
consistent hashing: 1796 of 10000 keys moved (18.0%)
theoretical minimum for consistent hashing removing 1 of 5 nodes: about 20.0%
```

Naive mod-N remaps nearly 8 in 10 keys for removing a single node out of 5. Consistent hashing lands within 2 percentage points of the theoretical minimum. Full details, including the distribution evenness numbers, are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metric that would matter most in production is per node key count, to catch skew early, along with a counter for how many keys moved on the last membership change.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Implementation | ns/op | allocs/op |
|---|---|---|
| Ring.Get, 150 replicas, 20 nodes | 208.8 | 1 |
| NaiveModN.Get, 20 nodes | 66.91 | 1 |

Ring costs about 3x more per lookup, the price of the binary search over virtual points. Full numbers, plus the distribution and remapping data, are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Property | Naive mod-N | Consistent hashing |
|---|---|---|
| Lookup cost | O(1), one hash and one modulo | O(log(nodes times replicas)), one hash and a binary search |
| Distribution evenness while stable | Very even, 6.3% spread in this lab's measurement | Less even, 57.6% spread at 150 replicas per node, worse with fewer replicas |
| Keys remapped when a node is added or removed | Nearly all of them, 79.5% measured here | Close to the theoretical minimum, 18.0% measured here against a 20% target |
| Implementation complexity | A few lines | A sorted structure plus virtual node bookkeeping |

The honest finding from this lab's own numbers: naive mod-N is actually the more evenly distributed option while the node set is stable. Consistent hashing trades some of that steady state evenness for dramatically less disruption when nodes come and go, which is the property that matters in any system where nodes actually do come and go, autoscaling, failures, planned capacity changes. If your node count is truly fixed forever, mod-N is simpler and better distributed. Almost nothing's node count is truly fixed forever.
