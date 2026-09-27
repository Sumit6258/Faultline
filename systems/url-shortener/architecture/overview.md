# Architecture

## Request flow, this version

```mermaid
sequenceDiagram
    participant C as Client
    participant Svc as Service
    participant RL as Rate limiter
    participant ID as ID generator
    participant Cache as LRU cache
    participant Store as Store (in memory)
    participant An as Analytics (async)

    C->>Svc: Shorten(clientID, longURL)
    Svc->>RL: Allow(clientID)
    RL-->>Svc: ok
    Svc->>ID: Generate()
    ID-->>Svc: code
    Svc->>Store: Save(code, longURL)
    Svc-->>C: code

    C->>Svc: Resolve(code)
    Svc->>Cache: Get(code)
    alt cache hit
        Cache-->>Svc: longURL
        Svc->>An: RecordClick(code), fire and forget
        Svc-->>C: longURL
    else cache miss
        Svc->>Store: Load(code)
        Store-->>Svc: longURL
        Svc->>Cache: Set(code, longURL)
        Svc->>An: RecordClick(code), fire and forget
        Svc-->>C: longURL
    end
```

## The evolution this system is built to show

```text
V0  Single process, in-memory store              <- this system, as built
V1  + LRU cache in front of the store             <- this system, as built
V2  + async analytics, abuse-prevention limiter   <- this system, as built
V3  + persistent store (Postgres), survives a restart      <- documented next step, not built
V4  + horizontal scaling: multiple stateless app servers    <- documented next step, not built
V5  + shared cache (Redis) instead of one per process       <- documented next step, not built
V6  + read replicas / sharded store for write and read scale <- documented next step, not built
V7  + multi-region                                <- documented next step, not built
```

This system currently occupies V0 through V2: a single process, but already cached, abuse-guarded, and asynchronous where the redirect path benefits from it. See the README's Scaling section for what V3 onward would actually require, and why V3 specifically was attempted and then deliberately not built in this sandbox.

## Why persistence stops at in-memory

A real Postgres-backed store was attempted for this repository's replication lab first. Postgres itself installs and runs fine in this sandbox, but a running server process does not reliably survive between separate tool invocations here, which makes depending on one across a multi-step build unreliable enough that it isn't used as this system's actual store. The Store interface in internal/store is written so a Postgres-backed implementation is a drop-in: anything satisfying Save, Load, and Exists works, the Service and everything above it never needs to change.

## Two bugs the load test itself caught

```mermaid
flowchart TD
    A[Load test creates 1000 URLs] --> B{Rate limiter capacity}
    B -->|too low, shared default| C[Most creates silently fail]
    C --> D[codes array full of empty strings]
    D --> E[Cache-size comparison shows no difference: bug, not a finding]
    B -->|raised for bulk setup| F[All 1000 creates succeed]
    F --> G[Cache-size comparison becomes meaningful]
```

```mermaid
flowchart TD
    A[40 requests from one client] --> B{Client identity}
    B -->|RemoteAddr with port| C[Every request looks like a new client, port changes each connection]
    C --> D[Rate limiter never triggers]
    B -->|IP only, port stripped| E[Requests correctly correlate]
    E --> F[Limiter blocks at exactly the configured burst]
```

Both are documented with their measured before-and-after in benchmarks/RESULTS.md.
