# Architecture

This document describes how Faultline is organized and why. For what's built so far, see [ROADMAP.md](ROADMAP.md).

## Technology strategy

Go is the primary language for distributed systems and infrastructure work: rate limiters, caches, coordination primitives, gateways. Its concurrency primitives (goroutines, channels, sync, context) map directly onto the problems this repository is about, and a single static binary is the simplest thing to run and load test.

Java is used for one enterprise transactional system, the payment platform, to demonstrate a different concurrency and transaction model than Go's.

Python is used for simulation, traffic generation, and analysis tooling, not for latency critical services.

TypeScript is used for the interactive visualization lab in web/interactive-lab.

SQL is treated as a first class language. Query plans and indexing decisions are shown with real EXPLAIN ANALYZE output, not hidden behind an ORM.

No technology is added because it looks good in a diagram. Every dependency in this repository answers two questions: what problem does it solve, and what does it cost to operate.

## Directory structure

```text
faultline/
├── docs/                  documentation by topic, plus ADRs and incident write-ups
├── labs/                  one focused pattern per folder: README, architecture, source, tests, benchmarks
├── systems/               complete systems assembled from multiple labs
├── benchmarks/            the aggregated results dashboard, generated from each lab's own results
├── infrastructure/        docker, kubernetes, terraform, and shared observability configuration
├── scripts/               capacity planning tools, traffic generators, the chaos harness
└── web/interactive-lab/   the TypeScript visualization app
```

## Lab catalog

| Category | Lab | Status |
|---|---|---|
| Resilience | Rate Limiting | done |
| Resilience | Circuit Breakers | planned |
| Resilience | Retries, Backoff, and Jitter | planned |
| Caching | Cache Aside and Eviction | done |
| Caching | Stampede Prevention | done |
| Coordination | Distributed Locks | planned |
| Coordination | Leader Election | planned |
| Coordination | Consensus (Raft) | planned |
| Data | Replication and Replication Lag | planned |
| Data | Sharding and Consistent Hashing | done |
| Messaging | Kafka: Partitions, Consumer Lag, DLQ | planned |

## System catalog

Tier 1, built first:

| System | Core patterns | Language |
|---|---|---|
| URL Shortener | ID generation, cache-aside, hot key handling | Go |
| Payment Platform | idempotency, outbox, saga, reconciliation | Java plus a Go gateway |
| Notification Platform | multi-channel fan-out, retry and DLQ | Go |
| Social Feed | fan-out on write versus fan-out on read | Go |

Tier 2, built after the core labs exist:

| System | Core patterns | Language |
|---|---|---|
| Ride Sharing | geospatial indexing, matching, trip state machine | Go and Python |
| Search Engine | inverted index, ranking, sharding | Go and Python |
| E-commerce Order Lifecycle | saga, inventory reservation | Java or Python |
| Object Storage | chunking, replication, checksums | Go |

Food Delivery is documented as a variant of Ride Sharing instead of built separately, since the geo-matching core is the same. Video Streaming and Ad Serving are deferred. Video needs real media infrastructure that doesn't fit a laptop setup, and ad serving's real-time bidding problem doesn't reuse much of the rest of the catalog.

## Documentation rule

Every doc in this repository, including this one, follows one rule: state what the thing is for, what breaks without it, what it costs, and what trade-off it introduces. See [docs/STYLE.md](docs/STYLE.md).
