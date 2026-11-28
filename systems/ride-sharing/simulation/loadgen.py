#!/usr/bin/env python3
"""Drives a running ride-sharing server the way phones would: drivers ping
their location and poll for offers, riders ask for trips, and everyone steps
through the trip at a sped-up pace. It exists to make the service do real
work against real HTTP, so the metrics, the dashboards and the failure
handling can be seen moving, and it reports what the riders experienced.

Standard library only.

    python3 simulation/loadgen.py --base http://127.0.0.1:8080 --seconds 30
"""

import argparse
import heapq
import http.client
import json
import math
import random
import time
import urllib.parse

CENTER = (12.97, 77.59)


def percentile(values, q):
    """The q-th percentile (0 to 100) by linear interpolation, or None for no data."""
    if not values:
        return None
    s = sorted(values)
    k = (len(s) - 1) * q / 100
    lo, hi = int(math.floor(k)), int(math.ceil(k))
    return s[lo] + (s[hi] - s[lo]) * (k - lo)


class Client:
    def __init__(self, base):
        u = urllib.parse.urlparse(base)
        self.host, self.port = u.hostname, u.port or 80
        self.conn = http.client.HTTPConnection(self.host, self.port, timeout=10)
        self.requests = 0
        self.statuses = {}

    def call(self, method, path, body=None, headers=None):
        data = json.dumps(body) if body is not None else None
        h = dict(headers or {})
        if data:
            h["Content-Type"] = "application/json"
        for attempt in range(2):  # a kept-alive connection can have been closed by the server
            try:
                self.conn.request(method, path, body=data, headers=h)
                resp = self.conn.getresponse()
                raw = resp.read()
                break
            except (http.client.HTTPException, ConnectionError, OSError):
                self.conn.close()
                self.conn = http.client.HTTPConnection(self.host, self.port, timeout=10)
                if attempt:
                    raise
        self.requests += 1
        self.statuses[resp.status] = self.statuses.get(resp.status, 0) + 1
        return resp.status, (json.loads(raw) if raw else None)


def offset(lat, lon, north_m, east_m):
    return lat + north_m / 111195.0, lon + east_m / (111195.0 * math.cos(math.radians(lat)))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--seconds", type=float, default=30)
    ap.add_argument("--drivers", type=int, default=25)
    ap.add_argument("--riders-per-minute", type=float, default=40)
    ap.add_argument("--accept-probability", type=float, default=0.85)
    ap.add_argument("--ignore-probability", type=float, default=0.05, help="drivers who never answer, so offers lapse")
    ap.add_argument("--radius-m", type=float, default=3000, help="how far from the center drivers and riders are spread")
    ap.add_argument("--seed", type=int, default=1)
    args = ap.parse_args()

    rng = random.Random(args.seed)
    api = Client(args.base)
    events, seq = [], 0
    t0 = time.monotonic()

    def at(delay, fn, *a):
        nonlocal seq
        seq += 1
        heapq.heappush(events, (time.monotonic() + delay, seq, fn, a))

    def point(radius):
        r, th = radius * math.sqrt(rng.random()), rng.random() * 2 * math.pi
        return offset(CENTER[0], CENTER[1], r * math.sin(th), r * math.cos(th))

    trips = {}  # trip id -> the rider's view
    done_at = t0 + args.seconds
    pos = {}

    def ping(d):
        lat, lon = pos[d]
        pos[d] = offset(lat, lon, rng.uniform(-40, 40), rng.uniform(-40, 40))
        api.call("POST", f"/drivers/{d}/location", {"lat": pos[d][0], "lon": pos[d][1]})
        if time.monotonic() < done_at:
            at(4.0, ping, d)

    def poll(d):
        status, offer = api.call("GET", f"/drivers/{d}/offer")
        if status == 200:
            roll = rng.random()
            if roll < args.ignore_probability:
                pass  # never answers
            elif roll < args.ignore_probability + (1 - args.ignore_probability) * args.accept_probability:
                at(rng.uniform(1, 4), respond, d, offer["trip_id"], True, offer["pickup_distance_meters"])
            else:
                at(rng.uniform(1, 3), respond, d, offer["trip_id"], False, 0)
        if time.monotonic() < done_at + 30:
            at(1.0, poll, d)

    def respond(d, trip_id, accept, pickup_m):
        status, _ = api.call("POST", f"/drivers/{d}/trips/{trip_id}/{'accept' if accept else 'decline'}")
        if accept and status == 200:
            at(min(pickup_m / 40.0, 6) + 0.5, step, d, trip_id, "arrive")

    def step(d, trip_id, what):
        status, _ = api.call("POST", f"/drivers/{d}/trips/{trip_id}/{what}")
        nxt = {"arrive": ("start", 1.0), "start": ("complete", rng.uniform(5, 10))}.get(what)
        if nxt and status == 200:
            at(nxt[1], step, d, trip_id, nxt[0])

    def request(i):
        pick, drop = point(args.radius_m), point(args.radius_m)
        status, tr = api.call("POST", "/trips", {"rider": f"rider{i}", "from": {"lat": pick[0], "lon": pick[1]}, "to": {"lat": drop[0], "lon": drop[1]}},
                              {"Idempotency-Key": f"load-{args.seed}-{i}"})
        if status in (200, 201):
            trips[tr["id"]] = {"rider": f"rider{i}"}

    for i in range(args.drivers):
        d = f"driver{i:03d}"
        pos[d] = point(args.radius_m)
        at(rng.uniform(0, 1), ping, d)
        at(rng.uniform(0.2, 1.2), poll, d)

    t, i = 0.0, 0
    while t < args.seconds:
        t += rng.expovariate(args.riders_per_minute / 60.0)
        if t < args.seconds:
            at(t, request, i)
            i += 1

    while events and time.monotonic() < done_at + 30:
        when, _, fn, a = events[0]
        wait = when - time.monotonic()
        if wait > 0:
            time.sleep(min(wait, 0.2))
            continue
        heapq.heappop(events)
        fn(*a)
        if time.monotonic() >= done_at and not any(f is not poll for _, _, f, _ in events):
            break

    outcomes, waits = {}, []
    for tid in trips:
        status, tr = api.call("GET", f"/trips/{tid}")
        if status != 200:
            continue
        outcomes[tr["state"]] = outcomes.get(tr["state"], 0) + 1
        accepted = next((e for e in tr.get("history", []) if e["kind"] == "accept"), None)
        if accepted:
            created = time.mktime(time.strptime(tr["created_at"][:19], "%Y-%m-%dT%H:%M:%S"))
            at_ = time.mktime(time.strptime(accepted["at"][:19], "%Y-%m-%dT%H:%M:%S"))
            waits.append(max(0.0, at_ - created))

    p = lambda q: "n/a" if not waits else f"{percentile(waits, q):.1f}s"
    print(f"trips requested   {len(trips)}")
    print(f"outcomes          {dict(sorted(outcomes.items()))}")
    print(f"match wait        p50 {p(50)}, p95 {p(95)} (whole seconds, from the server's own timestamps)")
    print(f"http requests     {api.requests}, statuses {dict(sorted(api.statuses.items()))}")
    errors = sum(n for s, n in api.statuses.items() if s >= 500)
    print(f"server errors     {errors}")
    raise SystemExit(1 if errors else 0)


if __name__ == "__main__":
    main()
