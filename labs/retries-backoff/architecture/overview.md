# Architecture

## The storm

```mermaid
flowchart TB
    E[Shared dependency blips] --> C1[Client 1 fails]
    E --> C2[Client 2 fails]
    E --> CN[Client N fails]
    C1 --> W1["wait(fixed) = 200ms"]
    C2 --> W2["wait(fixed) = 200ms"]
    CN --> WN["wait(fixed) = 200ms"]
    W1 --> R[All N retry at the same instant]
    W2 --> R
    WN --> R
    R --> E2[Dependency, barely recovering, gets hit by all N at once]
```

## The fix

```mermaid
flowchart TB
    E[Shared dependency blips] --> C1[Client 1 fails]
    E --> C2[Client 2 fails]
    E --> CN[Client N fails]
    C1 --> J1["wait = random(0, 200ms)"]
    C2 --> J2["wait = random(0, 200ms)"]
    CN --> JN["wait = random(0, 200ms)"]
    J1 --> R[Retries spread across the full 200ms window]
    J2 --> R
    JN --> R
    R --> E2[Dependency sees a ramp, not a spike]
```

Every client still retries within the same overall budget, up to 200ms. The only change is that each one picks its own moment inside that window instead of all picking the same one. Measured effect is in benchmarks/RESULTS.md.
