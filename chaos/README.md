# Chaos engineering

Faults injected into the running services from outside, to check that what the code claims survives what production does to it. The question each experiment asks is not "does it work", which the unit tests answer, but "does it still work when something goes wrong while it is working".

## The approach

Every experiment follows the same shape, which is what makes it an experiment and not a demo:

1. **A hypothesis**, stated before the fault, about what should stay true. "A crash loses no acknowledged payment."
2. **A real fault**, applied from outside the way a failure would arrive: a process killed with SIGKILL so it cannot clean up, a process frozen with SIGSTOP, a response dropped on the network, a database lock held by another process. Nothing asks the service to cooperate.
3. **A check against ground truth.** The result is read from the database and from the metrics, never from what the service says about itself. A service that has just been crashed is exactly the thing whose account of itself should not be taken on trust.
4. **A control where it matters.** The lost response experiment also runs a client without idempotency keys, to show the failure the keys prevent. An experiment that cannot reproduce the problem proves nothing about the fix.

## What is here

| | |
|---|---|
| `proxy/` | A fault injecting HTTP proxy in Go. It can add latency, answer with an error, drop a request before the service sees it, drop the response after the service has finished the work, or hold a request until the client gives up. The fault is set at runtime through a small API, and the proxy publishes its own metrics from the client's side. |
| `harness/` | The experiments, in Python with no dependencies: a process manager that can crash, freeze and restart services, a client that reports what a failure looked like from outside, checks that read the database directly, and the runner. |
| `RESULTS.md` | The output of a full run. |
| `systems/payment-platform/app/relay_worker.py` | The outbox relay as its own process, so it can be crashed or frozen on its own. The API runs it on a thread by default. `PAYMENT_RELAY=external` hands it to the worker. |

## Run it

From the repository root, with the payment platform's requirements installed and `go` on your path:

```bash
python3 -m chaos.harness.run --list
python3 -m chaos.harness.run                         # all of them, about two minutes
python3 -m chaos.harness.run --only lost-response    # one
python3 -m chaos.harness.run --report chaos/RESULTS.md
```

Set `PROMETHEUS` to a `prometheus` binary and the relay stall experiment also checks that the real alert fires and then resolves. Set `CHAOSPROXY` to use a prebuilt proxy instead of building one. Every service listens on a free port, so nothing collides with what you already have running.

## The experiments

| Experiment | Hypothesis | What a run showed |
|---|---|---|
| `lost-response` | A payment that executed but whose response was lost is charged once when the client retries with its key, and twice when it retries with a fresh one. | 40 of 40 recovered by the idempotent retry. The control double charged 40 of 40. |
| `crash-under-load` | Killing the API with SIGKILL, repeatedly, mid burst, loses no acknowledged payment, charges none twice, leaves none unannounced. | Zero violations across five crashes and thousands of payments. Several payments had committed with their acknowledgement lost to the crash, and the retries were recognised as repeats. |
| `relay-stall` | A stalled relay does not stop payments. The backlog and its age show in the metrics. Everything is announced exactly once on resume. | All payments accepted during the stall. `PaymentOutboxStuck` fired about 36 seconds in, 30 seconds of age plus the alert's 5 second delay in this run, and resolved after the drain. |
| `relay-crash-duplicates` | Crashing the relay repeatedly never loses an event, but does publish some twice. | No event lost, in every run. Duplicates: 9 to 14 over 26 to 30 kills, in five runs. That is roughly the share of each event's simulated broker round trip that falls after the broker accepted it, which is the window a kill has to land in. |
| `db-write-lock` | Under a held write lock, requests wait and succeed or fail cleanly after the busy timeout, leave no partial state, metrics keep working, and the service recovers at once. | 8 of 12 waited and succeeded, 4 failed with 500 after 10 seconds. `/metrics`, scraped in the middle of the lock, answered in a few milliseconds. |
| `latency-invisible-to-the-service` | Delay added on the network moves what users see but not the service's own latency histogram. | Client mean 4ms to 232ms. The service's histogram, 2.2ms to 3.2ms. The proxy's, measured where clients are, 230ms. |

## What it found

**Idempotency works under the failure it exists for, and the control shows the cost of not having it.** The interesting number is not 40 of 40. It is that 40 payments were executed that the client never saw succeed, which is the normal state of affairs on a network that loses responses.

**At least once publishing is real, and measurable.** Killing the relay between publishing an event and marking it dispatched duplicates exactly that one event, and never loses one. That is the guarantee the outbox pattern gives, and the reason whatever consumes the broker must tolerate repeats, which is the job of the idempotent consumer in `labs/kafka-messaging`.

**A held database lock fails requests with a plain 500 after 10 seconds.** The failure is clean, nothing partial is left behind, and the service recovers instantly. But it is a 500, not a 503, so a client cannot tell it is worth retrying, and each blocked request holds a worker thread for the whole 10 seconds. The worker pool Starlette runs synchronous endpoints on is, by default, 40 threads. A lock that outlasts the busy timeout while requests keep arriving could use them all up, and then even requests that never touch the database would wait. That was not tested here: the experiment sends one request a second. It is the first thing to try next, and a shorter timeout answered with a 503 and a retry hint is the likely fix.

**The service cannot see this kind of slowness.** The latency experiment shows a 230 millisecond delay that the service's own histogram did not register. An alert on the service's own latency would have stayed quiet while users waited. The proxy's histogram caught it because it measures at the edge. Anything in front of a service, a load balancer, a network, a queue, is invisible to that service.

## How the harness itself was checked

An experiment that passes is only worth something if it could have failed. So the experiments were run against deliberately broken versions of the payment platform:

| Bug injected | Experiment | Caught |
|---|---|---|
| Replay detection broken, a retried key answered as if new | `lost-response` | yes, 1 of 1 |
| Dual write: the event committed in a second transaction 2ms after the payment | `crash-under-load` | yes, 3 of 3 runs, with several payments left unannounced each time |
| The same dual write with no gap between the two commits | `crash-under-load` | yes, 4 of 4 runs |
| Relay marks an event dispatched before publishing it | `relay-crash-duplicates` | yes, 3 of 3 runs, 9 to 13 events lost each time |

Those are small samples, and chaos testing is probabilistic by nature: a crash has to land in the window to find the bug. A pass means the experiment did not find a problem on that run, not that none exists. The proxy has tests for every fault mode, and fifteen deliberate breaks of it were each caught. The harness's helpers have 28 tests.

Two things the checks turned up are worth saying. The simulated broker delay first sat entirely after the event was published, so a relay that marked events early would never have been caught. It is now split before and after. And the relay stall experiment first stopped watching for the alert at a fixed time that happened to coincide with the moment the alert fired, so it passed once and failed once. It now waits for the alert instead of guessing how long that takes.

## What this does not show

SIGKILL ends a process, not a machine. The operating system still has everything the process wrote, so these experiments cannot show what a power cut does to a database. The faults are at the HTTP layer, not the packet layer: no partial writes, reordering or asymmetric partitions. Everything runs on one machine.

Only the payment platform is exercised, because it is the only service with durable state. The others keep everything in memory by design, so crashing one simply loses everything, which is a stated limit and not a discovery. The labs are libraries and demos with nothing to crash.

## Not built

Resource exhaustion (a full disk, no memory), clock skew, partitions between several services, and experiments for the Kafka lab beyond the consumer lag alert it already has in `observability/`.
