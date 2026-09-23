# Load test results

Measured against the demo server running in this sandbox on localhost, not a real network. Reproduce with:

```bash
go build -o rl-server ./cmd/server
go build -o rl-loadtest ./load-tests
./rl-server -algo=token-bucket -limit=10 &
./rl-loadtest -url=http://localhost:8080 -clients=20 -n=5 -shared-client -client-id-prefix=alice
```

## Scenario 1: burst against a token bucket

Token bucket, capacity 10, refill rate 10 per second. One logical client (alice) fires 100 requests across 20 concurrent connections, as fast as the local network stack allows.

```
clients=20 requests_per_client=5 total_requests=100 shared_client=true
allowed=10 rejected=90 errored=0
elapsed=13.6ms throughput=7345 req/s
```

Exactly 10 requests got through, the bucket's starting capacity. The other 90 were rejected with 429 in under 14 milliseconds. That's the point of a token bucket: it caps the burst at capacity no matter how many connections the client opens at once.

## Scenario 2: the distributed failure mode

This is the demonstration that matters most. Rate limiting state that lives in one process's memory only limits that one process.

Setup: three independent server instances, each running FixedWindow with limit 10 over a 10 second window, no shared state between them, standing in for three nodes behind a load balancer with no shared cache. One client (alice) sends 30 requests, round robined evenly across the three nodes, 10 to each.

```
30 requests, round robined across 3 nodes, each enforcing limit=10 independently:
allowed=30 rejected=0

the same 30 requests against a single node with the same limit=10:
allowed=10 rejected=20
```

The operator configured limit=10 meaning to cap this client at 10 requests. Because each node only sees its own third of the traffic and has no idea the other two nodes exist, all 30 requests were let through, three times the intended limit. The single node run shows what was actually intended: 10 allowed, 20 rejected.

This is why a distributed deployment needs the limiter state in a shared store, Redis being the usual choice, rather than in each process's own memory. That variant isn't built in this lab yet. See the Scaling section of the main README.
