# Faultline

A hands-on laboratory for the patterns that hold large-scale systems together, and for what happens when they don't.

Every lab in this repository is runnable, load-testable, and breakable. You start a service, throw real traffic at it, and read the numbers it produces. The goal isn't to be able to explain consistent hashing in an interview. It's to have actually built one, watched it redistribute keys when a node dropped out, and measured how much moved.

## What this is

A public, polyglot engineering repository. Go is the primary language for distributed systems and infrastructure code. Java, Python, and TypeScript are used where they fit a specific problem, not for variety. Every lab ships with real tests, real benchmarks, and an honest label on any number that wasn't actually measured.

## What this isn't

Not a system design interview cheat sheet. Not a collection of architecture diagrams with no code behind them. Not a place where "production-grade" means a README that says so.

## Status

Phase 1 is done. Phase 2 is in progress, three of six core labs are live so far. See [ROADMAP.md](ROADMAP.md) for what's next.

## Quickstart

```bash
git clone https://github.com/Sumit6258/Faultline.git
cd faultline
make setup
make test
```

`make setup` checks for Go and installs it on a Debian or Ubuntu based system if it's missing. `make test` runs the full suite for every lab that exists so far.

## Labs

| Lab | What it teaches | Language |
|---|---|---|
| [Rate Limiting](labs/rate-limiting) | Fixed window, sliding window, token bucket, and leaky bucket, plus why per-node counters fail behind a load balancer | Go |
| [Caching](labs/caching) | Cache-aside with TTL and LRU eviction, and request coalescing to stop a cache stampede | Go |
| [Consistent Hashing](labs/consistent-hashing) | A hash ring with virtual nodes, measured against naive modulo hashing when a node is added or removed | Go |
| [Distributed Locks](labs/distributed-locks) | Time bounded leases and fencing tokens, and exactly why a stale lock holder can still corrupt data without them | Go |
| [Leader Election](labs/leader-election) | Heartbeat based election: a leader that renews normally, then crashes, then a new election | Go |
| [Circuit Breakers](labs/circuit-breakers) | The closed, open, half-open cycle, including keeping concurrent callers from flooding a half-open trial | Go |

More labs and full systems are planned. See [ARCHITECTURE.md](ARCHITECTURE.md) for the full catalog and [ROADMAP.md](ROADMAP.md) for build order.

## Repository map

```text
faultline/
├── docs/                  documentation by topic, plus ADRs and incident write-ups
├── labs/                  one focused pattern per folder
├── systems/               complete systems assembled from multiple labs
├── benchmarks/            aggregated results across labs
├── infrastructure/        docker, kubernetes, terraform, observability configs
├── scripts/               capacity planning tools, traffic generators, chaos tooling
└── web/interactive-lab/   browser-based visualizations
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Every lab follows the same definition of done: a real implementation, real tests, a labeled benchmark, and at least one demonstrated failure mode.

## License

MIT. See [LICENSE](LICENSE).
