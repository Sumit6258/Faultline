# Architecture

## Request flow, single node

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant L as Limiter, in memory

    C->>S: GET / with X-Client-ID header
    S->>L: Allow(clientID)
    alt under the limit
        L-->>S: true
        S-->>C: 200 OK
    else over the limit
        L-->>S: false
        S-->>C: 429 Too Many Requests, Retry-After: 1
    end
```

## Failure mode: independent nodes, no shared state

```mermaid
flowchart LR
    C[Client alice] --> LB[Load balancer, round robin]
    LB --> N1[Node 1, limit=10]
    LB --> N2[Node 2, limit=10]
    LB --> N3[Node 3, limit=10]
```

Each node enforces its own limit correctly. The service as a whole does not, since none of the three nodes know the other two exist. Measured effect is in load-tests/RESULTS.md.

## What a fix looks like

```mermaid
flowchart LR
    C[Client alice] --> LB[Load balancer, round robin]
    LB --> N1[Node 1]
    LB --> N2[Node 2]
    LB --> N3[Node 3]
    N1 --> R[(Shared Redis counter)]
    N2 --> R
    N3 --> R
```

Moving the counter into Redis, INCR plus EXPIRE for fixed window, or a small Lua script for token bucket to keep the check and decrement atomic, makes the limit correct across any number of nodes. The cost is a network round trip per request, and Redis becoming a hard dependency: if it's unreachable, the service has to pick between failing open or failing closed. This variant is on the roadmap for this lab, not built yet.
