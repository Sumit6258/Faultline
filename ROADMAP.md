# Roadmap

## Phase 1: Foundation (in progress)

- [x] Repository skeleton, Makefile, docs/STYLE.md
- [x] Rate Limiting lab
- [x] Caching lab
- [x] Consistent Hashing lab

## Phase 2: Core labs (done)

- [x] Distributed Locks
- [x] Leader Election
- [x] Circuit Breakers
- [x] Retries, Backoff, and Jitter
- [x] Replication and Replication Lag (simulated, see labs/replication's README)
- [x] Kafka Messaging and Consumer Lag (simulated, see labs/kafka-messaging's README)

## Phase 3: First systems (done)

- [x] URL Shortener
- [x] Payment Platform, part 1: idempotency and outbox (Python/FastAPI, see systems/payment-platform's README for why)

## Phase 4: Interactive lab (in progress)

- [x] Client-side visualizations: consistent hashing ring, rate limiter algorithms, circuit breaker state machine (web/interactive-lab, 32 tests, not yet visually verified in a real browser, see its README)
- [ ] Backend-driven visualizations (real consumer lag from a running labs/kafka-messaging instance, and similar)
- [ ] Notification Platform
- [ ] Social Feed

## Phase 5: Operability retrofit

- [ ] Shared observability kit applied across everything built so far
- [ ] Chaos harness
- [ ] Benchmark aggregator

## Phase 6: Remaining systems

- [ ] Ride Sharing
- [ ] Search Engine
- [ ] E-commerce
- [ ] Consensus (Raft) and quorum reads and writes

Video Streaming and Ad Serving are intentionally not on this roadmap. See [ARCHITECTURE.md](ARCHITECTURE.md) for why.
