# Architecture

## The full cycle

```mermaid
stateDiagram-v2
    [*] --> Closed
    Closed --> Closed: success
    Closed --> Open: consecutiveFails >= threshold
    Open --> Open: call arrives before cooldown elapses, rejected fast
    Open --> HalfOpen: call arrives after cooldown elapses
    HalfOpen --> Closed: trial call succeeds
    HalfOpen --> Open: trial call fails
```

## What each call does

```mermaid
flowchart TD
    C[Call arrives] --> S{Current state}
    S -->|Closed| Run[Run the dependency]
    S -->|Open, cooldown not elapsed| Reject[Return ErrCircuitOpen, dependency never touched]
    S -->|Open, cooldown elapsed| Trial[Move to HalfOpen, run one trial call]
    S -->|HalfOpen, trial already in flight| Reject
    Run --> R{Result}
    Trial --> R
    R -->|success| Close[State becomes Closed, failure count resets]
    R -->|failure| Count[Increment failure count]
    Count --> T{Over threshold, or was this the trial}
    T -->|yes| OpenIt[State becomes Open]
    T -->|no| StayClosed[State stays Closed]
```

The half-open state exists specifically to stop a flood of requests from all hitting a barely-recovered dependency at once. Only one trial call is allowed through; every other concurrent caller gets rejected until that trial resolves. See breaker.go's halfOpenInFlight field and the concurrency test in breaker_test.go.
