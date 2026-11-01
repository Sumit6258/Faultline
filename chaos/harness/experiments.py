"""The experiments. Each one states a hypothesis about how the system should
behave under a fault, applies the fault from outside, and checks the result
against the database and the metrics, not against what the service says about
itself.

An experiment is a function that takes a Context and returns a Result. The
registry at the bottom is what run.py lists and runs.
"""

import collections
import contextlib
import json
import random
import sqlite3
import threading
import time
import traceback
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

from .invariants import check_payments, read_state
from .client import get_metric, request
from .payments import PaymentStack, new_key
from .prometheus import PrometheusRun
from .procs import Stack


@dataclass
class Context:
    workdir: Path
    prometheus: str | None = None  # path to a prometheus binary, enables the alert checks
    promtool: str | None = None


@dataclass
class Result:
    name: str
    hypothesis: str
    checks: list[tuple[bool, str]] = field(default_factory=list)
    findings: list[str] = field(default_factory=list)  # what was measured, in plain words
    notes: list[str] = field(default_factory=list)  # limits of what this experiment shows
    seconds: float = 0.0
    error: str = ""

    def expect(self, condition: bool, message: str) -> bool:
        self.checks.append((bool(condition), message))
        return bool(condition)

    @property
    def passed(self) -> bool:
        return not self.error and bool(self.checks) and all(ok for ok, _ in self.checks)


EXPERIMENTS: dict[str, tuple[str, Callable]] = {}


def experiment(name: str, hypothesis: str):
    def register(fn):
        def run(ctx: Context) -> Result:
            result = Result(name, hypothesis)
            start = time.time()
            try:
                fn(ctx, result)
            except Exception:
                result.error = traceback.format_exc()
            result.seconds = time.time() - start
            return result

        EXPERIMENTS[name] = (hypothesis, run)
        return fn

    return register


def charges_per_amount(db: str) -> collections.Counter:
    payments, *_ = read_state(db)
    return collections.Counter(payments.values())


# ----------------------------------------------------------------------------


@experiment(
    "lost-response",
    "When a payment is executed but its response is lost, a client that retries with the same idempotency key is charged once, "
    "and a client that retries with a fresh key is charged twice.",
)
def lost_response(ctx: Context, r: Result) -> None:
    n_same, n_naive, n_lost = 40, 40, 20
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack, proxy=True).start()
        acked: dict[str, int] = {}
        attempted: set[str] = set()

        # A. The response is lost. The client retries with the SAME key.
        ps.set_fault(mode="drop-response", path_prefix="/payments", methods=["POST"])
        a_keys = {new_key(): 1000 + i for i in range(n_same)}
        first = {k: ps.pay(k, amt, timeout=1.5) for k, amt in a_keys.items()}
        attempted |= set(a_keys)
        ps.clear_fault()
        retry = {k: ps.pay(k, amt) for k, amt in a_keys.items()}
        acked.update({k: a_keys[k] for k, o in retry.items() if o.ok})

        unanswered = sum(not o.answered for o in first.values())
        replayed = sum(o.ok and o.body["created"] is False for o in retry.values())
        created_again = sum(o.ok and o.body["created"] is True for o in retry.values())
        a_charges = collections.Counter(read_state(ps.db)[0][k] for k in a_keys if k in read_state(ps.db)[0])
        r.findings.append(
            f"Same key: {unanswered} of {n_same} first attempts got no answer, yet {replayed} retries were told created=false, so the work had "
            f"been done all along. The database holds {sum(a_charges.values())} charges for {n_same} payments."
        )
        r.expect(unanswered == n_same, f"the fault dropped every first response ({unanswered}/{n_same})")
        r.expect(replayed == n_same and created_again == 0, f"every retry with the same key was recognised as a repeat ({replayed}/{n_same}), none created a second payment")
        r.expect(all(c == 1 for c in a_charges.values()) and sum(a_charges.values()) == n_same, "each payment was charged exactly once")

        # B. The control: the same loss, but the client retries with a NEW key, as a client without idempotency would.
        ps.set_fault(mode="drop-response", path_prefix="/payments", methods=["POST"])
        b_amounts = [5000 + i for i in range(n_naive)]
        for amt in b_amounts:
            k = new_key()
            attempted.add(k)
            ps.pay(k, amt, timeout=1.5)
        ps.clear_fault()
        for amt in b_amounts:
            k = new_key()
            attempted.add(k)
            o = ps.pay(k, amt)
            if o.ok:
                acked[k] = amt
        per_amount = charges_per_amount(ps.db)
        double = sum(1 for amt in b_amounts if per_amount[amt] == 2)
        r.findings.append(f"Fresh key (the control): {double} of {n_naive} payments were charged twice, the failure idempotency keys prevent.")
        r.expect(double == n_naive, f"without a shared key, retrying after a lost response double charged every payment ({double}/{n_naive}), so the experiment does reproduce the problem")

        # C. The request is lost before it reaches the service. The retry has to actually do the work.
        ps.set_fault(mode="drop-request", path_prefix="/payments", methods=["POST"])
        c_keys = {new_key(): 9000 + i for i in range(n_lost)}
        for k, amt in c_keys.items():
            ps.pay(k, amt, timeout=1.5)
        attempted |= set(c_keys)
        executed_early = sum(1 for k in c_keys if k in read_state(ps.db)[0])
        ps.clear_fault()
        retry_c = {k: ps.pay(k, amt) for k, amt in c_keys.items()}
        acked.update({k: c_keys[k] for k, o in retry_c.items() if o.ok})
        created_now = sum(o.ok and o.body["created"] is True for o in retry_c.values())
        r.findings.append(f"Lost request: {executed_early} of {n_lost} had reached the service, and {created_now} retries had to create the payment themselves.")
        r.expect(executed_early == 0 and created_now == n_lost, "a request lost before the service leaves nothing behind, and the retry creates the payment")

        report = check_payments(ps.db, acked, attempted)
        r.expect(report.ok, "database invariants hold: " + ("no violations" if report.ok else "; ".join(report.violations[:3])))
        r.findings.append(f"Final state: {report.counts['payments']} payments, {report.counts['events']} events, {report.counts['in_doubt']} executed without the client ever seeing an answer.")
        r.notes.append("The fault is applied at the HTTP layer by a proxy. It models a lost response, not a machine that loses power.")


def wait_until(condition, timeout: float, every: float = 0.1) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if condition():
            return True
        time.sleep(every)
    return condition()


def sink_keys(path: Path) -> list[str]:
    if not path.exists():
        return []
    return [json.loads(line)["idempotency_key"] for line in path.read_text().splitlines() if line.strip()]


# ----------------------------------------------------------------------------


@experiment(
    "crash-under-load",
    "Killing the API with SIGKILL, repeatedly, in the middle of a burst of payments loses no acknowledged payment, charges none twice, and leaves "
    "none unannounced, provided clients retry with the same key.",
)
def crash_under_load(ctx: Context, r: Result) -> None:
    workers, before, after, crashes = 8, 2.0, 1.5, 5
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack).start()
        stop = threading.Event()
        lock = threading.Lock()
        acked: dict[str, int] = {}
        ack_at: dict[str, float] = {}
        replayed_after_failure: list[str] = []
        attempted: set[str] = set()
        failures: collections.Counter = collections.Counter()

        def worker(w: int) -> None:
            i = 0
            while not stop.is_set():
                key, amount = new_key(), 100 + w * 100000 + i
                i += 1
                with lock:
                    attempted.add(key)
                for attempt in range(40):  # the same key until an answer arrives
                    o = ps.pay(key, amount, timeout=1.0)
                    if o.ok:
                        with lock:
                            acked[key], ack_at[key] = amount, time.time()
                            if attempt > 0 and o.body["created"] is False:
                                replayed_after_failure.append(key)
                        break
                    with lock:
                        failures[o.kind] += 1
                    time.sleep(0.1)

        threads = [threading.Thread(target=worker, args=(w,)) for w in range(workers)]
        for t in threads:
            t.start()
        time.sleep(before)
        killed_at = time.time()
        downtimes = []
        for _ in range(crashes):  # several crashes, to catch more commits in flight
            downtimes.append(ps.restart_api())
            time.sleep(after)
        downtime = sum(downtimes)
        stop.set()
        for t in threads:
            t.join(timeout=60)

        drained = wait_until(lambda: read_state(ps.db)[2] == 0, 15)
        report = check_payments(ps.db, acked, attempted)
        durable_before_crash = sum(1 for t in ack_at.values() if t < killed_at)

        r.findings.append(
            f"{len(acked)} payments acknowledged, {durable_before_crash} of them before the first crash. The API was killed {crashes} times and was gone for "
            f"{downtime:.1f}s in total, during which clients saw {dict(failures)} and kept retrying."
        )
        r.findings.append(
            f"{len(replayed_after_failure)} payments had committed but their acknowledgement was lost to the crash. Their retries were recognised and answered "
            f"created=false instead of charging again."
        )
        r.expect(sum(failures.values()) > 0, "the crash was visible to clients, so the fault really landed")
        r.expect(len(acked) == len(attempted), f"every payment was eventually acknowledged ({len(acked)} of {len(attempted)})")
        r.expect(report.ok, "database invariants hold: " + ("no violations" if report.ok else "; ".join(report.violations[:3])))
        r.expect(drained, f"the restarted relay announced everything that was waiting ({report.counts['dispatched']} of {report.counts['events']} dispatched)")
        r.notes.append("SIGKILL ends the process but not the machine: the operating system still has everything the process wrote. This does not test a power cut.")


@experiment(
    "relay-stall",
    "When the relay stalls, payments keep succeeding, the backlog and its age are visible in the metrics, and everything is announced exactly once when the relay resumes.",
)
def relay_stall(ctx: Context, r: Result) -> None:
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack, relay="external").start()
        prom = PrometheusRun(stack, ctx.prometheus, "payment-platform", ps.api_port) if ctx.prometheus else None
        acked, attempted = {}, set()

        def pay_many(n: int, base: int) -> list:
            outcomes = []
            for i in range(n):
                k = new_key()
                attempted.add(k)
                o = ps.pay(k, base + i)
                outcomes.append(o)
                if o.ok:
                    acked[k] = base + i
                time.sleep(0.02)
            return outcomes

        pay_many(20, 100)
        healthy = wait_until(lambda: read_state(ps.db)[2] == 0 and len(sink_keys(ps.sink)) == 20, 15)
        r.expect(healthy, "steady state: with the relay running, every event is announced within moments")
        r.expect(prom is None or prom.alerts().get("PaymentOutboxStuck") is None, "steady state: PaymentOutboxStuck is not firing")

        ps.relay.pause()
        stall = 12
        stalled_at = time.time()
        outcomes = pay_many(100, 1000)
        accepted = sum(o.ok for o in outcomes)
        latencies = sorted(o.elapsed for o in outcomes)
        pending = ps.metric("payments_outbox_pending")
        r.expect(accepted == 100, f"payments kept succeeding while the relay was stalled ({accepted}/100), the API does not depend on it")
        r.expect(pending == 100, f"the backlog is visible in the metrics (payments_outbox_pending = {pending})")

        # Keep the relay stalled for as long as it takes. With Prometheus attached that means until the alert
        # fires, which is the age threshold (30s) plus the alert's delay, with a generous limit. Without it,
        # a fixed short stall.
        peak = 0.0
        alert_seen = None
        give_up = stalled_at + (60 if prom else stall)
        while time.time() < give_up:
            peak = max(peak, ps.metric("payments_outbox_oldest_pending_age_seconds") or 0.0)
            if prom:
                if alert_seen is None and prom.alerts().get("PaymentOutboxStuck") == "firing":
                    alert_seen = time.time() - stalled_at
                if alert_seen is not None and peak >= 31:
                    break
            time.sleep(0.5)
        r.findings.append(
            f"During {time.time() - stalled_at:.0f}s of stall: 100 of 100 payments accepted (p99 {latencies[98] * 1000:.0f}ms), backlog 100, "
            f"oldest event {peak:.0f}s old."
        )
        r.expect(peak >= (31 if prom else 10), f"the age of the oldest event grew as the stall went on ({peak:.0f}s)")
        if prom:
            r.expect(alert_seen is not None, f"PaymentOutboxStuck fired for a real stalled relay, {alert_seen:.0f}s into the stall" if alert_seen else "PaymentOutboxStuck never fired")
        else:
            r.notes.append("No Prometheus binary was given (set PROMETHEUS), so the stall was too short to reach the 30 second alert threshold and the alert itself was not checked.")

        ps.relay.resume()
        drained = wait_until(lambda: read_state(ps.db)[2] == 0, 20)
        published = sink_keys(ps.sink)
        dupes = len(published) - len(set(published))
        r.expect(drained, "after the relay resumed, the whole backlog was announced")
        r.expect(dupes == 0 and set(published) == set(acked), f"each event was published exactly once ({len(published)} lines for {len(acked)} payments)")
        if prom:
            cleared = prom.wait_for("PaymentOutboxStuck", None, 20)
            r.expect(cleared is not None, "the alert resolved once the backlog drained")
        report = check_payments(ps.db, acked, attempted)
        r.expect(report.ok, "database invariants hold: " + ("no violations" if report.ok else "; ".join(report.violations[:3])))
        r.notes.append("A paused process is not a crashed one. A pause cannot cause duplicates, which is why this one publishes each event exactly once.")


@experiment(
    "relay-crash-duplicates",
    "Crashing the relay with SIGKILL, over and over, never loses an event, but does publish some twice. Publishing is at least once, so a consumer has to cope with seeing an event again.",
)
def relay_crash_duplicates(ctx: Context, r: Result) -> None:
    n = 400
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack, relay="external", publish_delay_ms=15).start()
        ps.relay.pause()  # let a backlog build with no relay interfering
        acked, attempted = {}, set()
        for i in range(n):
            k = new_key()
            attempted.add(k)
            if ps.pay(k, 100 + i).ok:
                acked[k] = 100 + i
        ps.relay.resume()
        rng = random.Random(1)
        kills = 0
        while read_state(ps.db)[2] > 0 and kills < 60:
            time.sleep(rng.uniform(0.15, 0.45))
            if read_state(ps.db)[2] == 0:
                break
            ps.relay.kill9()
            kills += 1
            ps.relay.start()
        finished = wait_until(lambda: read_state(ps.db)[2] == 0, 30)

        published = sink_keys(ps.sink)
        unique = set(published)
        duplicates = len(published) - len(unique)
        missing = set(acked) - unique
        r.findings.append(
            f"The relay was killed {kills} times while draining {n} events. {len(published)} lines were published for {len(unique)} distinct events, "
            f"so {duplicates} were published twice (one per kill that landed between publishing an event and marking it)."
        )
        r.expect(finished, "the backlog was fully drained in the end")
        r.expect(not missing, f"no event was lost ({len(missing)} missing)")
        r.expect(kills >= 5 and duplicates >= 1, f"the crashes did cause duplicates ({duplicates}), so at least once publishing is real, not theoretical")
        r.expect(duplicates <= kills, f"a crash can duplicate at most the one event in flight ({duplicates} duplicates from {kills} kills)")
        report = check_payments(ps.db, acked, attempted)
        r.expect(report.ok, "database invariants hold: " + ("no violations" if report.ok else "; ".join(report.violations[:3])))
        r.notes.append("A 15ms simulated broker acknowledgement widens the window in which a kill causes a duplicate. With no delay the window is real but far narrower, and duplicates are rarer, never impossible.")


@experiment(
    "db-write-lock",
    "While something else holds the database's write lock, requests either wait and succeed or fail cleanly after the busy timeout, leave no partial state, "
    "metrics keep working, and the service recovers the moment the lock is released.",
)
def db_write_lock(ctx: Context, r: Result) -> None:
    lock_seconds, requests = 14.0, 12
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack).start()
        warm: dict[str, int] = {}
        for i in range(5):
            k = new_key()
            assert ps.pay(k, 50 + i).ok
            warm[k] = 50 + i
        wait_until(lambda: read_state(ps.db)[2] == 0, 10)

        holder = sqlite3.connect(ps.db, isolation_level=None, timeout=1)
        holder.execute("BEGIN IMMEDIATE")  # for example a backup or a migration
        t0 = time.time()
        results: list[tuple[int, float, object, str]] = []
        lock = threading.Lock()
        attempted: set[str] = set(warm)

        def one(i: int) -> None:
            time.sleep(i * 1.0)
            key = new_key()
            with lock:
                attempted.add(key)
            started = time.time() - t0
            o = ps.pay(key, 200 + i, timeout=30)
            with lock:
                results.append((i, started, o, key))

        threads = [threading.Thread(target=one, args=(i,)) for i in range(requests)]
        for t in threads:
            t.start()

        time.sleep(5)
        scrape_started = time.time()
        pending_during_lock = ps.metric("payments_outbox_pending")
        scrape_took = time.time() - scrape_started

        time.sleep(max(0, lock_seconds - (time.time() - t0)))
        holder.execute("COMMIT")
        holder.close()
        for t in threads:
            t.join(timeout=60)

        results.sort()
        ok = [x for x in results if x[2].ok]
        failed = [x for x in results if x[2].kind == "http_error"]
        other = [x for x in results if x[2].kind not in ("ok", "http_error")]
        r.findings.append(
            f"With the lock held for {lock_seconds:.0f}s and one request per second: {len(ok)} waited and succeeded, {len(failed)} failed with "
            f"{sorted({x[2].status for x in failed})} after about {min((x[2].elapsed for x in failed), default=0):.0f}s."
        )
        for i, started, o, _ in results:
            r.findings.append(f"  request {i:2d} started at +{started:4.1f}s: {o.kind} {o.status or ''} after {o.elapsed:5.1f}s")

        payments_now = read_state(ps.db)[0]
        r.expect(not other, "every request got an answer, none hung or lost its connection")
        r.expect(all(x[2].elapsed >= 9 for x in failed), "failures came only after the 10 second busy timeout, not instantly")
        r.expect(all(x[3] not in payments_now for x in failed), "a failed request left no payment behind: it failed cleanly, with no partial state")
        r.expect(len(ok) > 0 and len(failed) > 0, f"the lock was held long enough to see both outcomes ({len(ok)} succeeded, {len(failed)} failed)")
        r.expect(scrape_took < 3 and pending_during_lock is not None, f"/metrics kept answering during the lock ({scrape_took * 1000:.0f}ms), so observability survives a write lock")
        errors_counted = ps.metric("http_requests_total", method="POST", route="/payments", status="500") or 0
        r.expect(errors_counted == len(failed), f"the failures show up in http_requests_total as 500s ({errors_counted:.0f}), which is what the error alert watches")

        acked = {**warm, **{x[3]: 200 + x[0] for x in ok}}
        retry = {}
        for i, _, _, key in failed:
            o = ps.pay(key, 200 + i)
            retry[key] = o
            if o.ok:
                acked[key] = 200 + i
        r.expect(all(o.ok and o.body["created"] is True and o.elapsed < 1.0 for o in retry.values()), "after the lock was released, retrying the failed keys succeeded immediately")
        report = check_payments(ps.db, acked, attempted)
        r.expect(report.ok, "database invariants hold: " + ("no violations" if report.ok else "; ".join(report.violations[:3])))
        r.notes.append("The 10 second busy timeout is a decision in app/db.py. A shorter one would fail faster and free the request threads sooner, a longer one would ride out longer locks.")


@experiment(
    "latency-invisible-to-the-service",
    "Delay added on the network moves what users experience but not the service's own latency histogram. Only a measurement taken at the edge shows it.",
)
def latency_invisible(ctx: Context, r: Result) -> None:
    n = 60
    with Stack(ctx.workdir) as stack:
        ps = PaymentStack(stack, proxy=True).start()
        for i in range(10):
            ps.pay(new_key(), 1 + i)

        def server_sum_count() -> tuple[float, float]:
            m = ps.api_metrics()
            return (get_metric(m, "http_request_duration_seconds_sum", method="POST", route="/payments") or 0.0,
                    get_metric(m, "http_request_duration_seconds_count", method="POST", route="/payments") or 0.0)

        def burst(base: int) -> tuple[float, float]:
            (s0, c0), t0 = server_sum_count(), time.time()
            for i in range(n):
                assert ps.pay(new_key(), base + i, timeout=5).ok
            client_mean = (time.time() - t0) / n
            (s1, c1) = server_sum_count()
            return client_mean, (s1 - s0) / (c1 - c0)

        client_before, server_before = burst(1000)
        ps.set_fault(mode="latency", latency_ms=200, jitter_ms=50, path_prefix="/payments")
        client_after, server_after = burst(2000)
        import urllib.request

        with urllib.request.urlopen(ps.control_url + "/metrics", timeout=5) as resp:
            text = resp.read().decode()
        edge_sum = get_metric(text, "chaosproxy_request_duration_seconds_sum", fault="latency")
        edge_count = get_metric(text, "chaosproxy_request_duration_seconds_count", fault="latency")
        edge_mean = edge_sum / edge_count

        r.findings.append(f"Client mean latency went from {client_before * 1000:.0f}ms to {client_after * 1000:.0f}ms.")
        r.findings.append(f"The service's own histogram for POST /payments went from {server_before * 1000:.1f}ms to {server_after * 1000:.1f}ms.")
        r.findings.append(f"The proxy's histogram, measured at the edge where clients are, reports {edge_mean * 1000:.0f}ms.")
        r.expect(client_after - client_before >= 0.15, f"users waited {1000 * (client_after - client_before):.0f}ms longer")
        r.expect(server_after - server_before < 0.02, f"the service's histogram barely moved ({1000 * (server_after - server_before):.1f}ms), it cannot see delay added before the request arrives")
        r.expect(edge_mean >= 0.2, "a histogram at the edge does show the delay")
        r.notes.append("This is why alerts on a service's own latency are not enough on their own: a slow network, load balancer or queue in front of it is invisible to it.")
