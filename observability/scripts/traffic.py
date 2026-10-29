#!/usr/bin/env python3
"""Sends a realistic mix of traffic to every Faultline service, so the
dashboards have something to show and the alerts something to react to.

    python3 observability/scripts/traffic.py --seconds 120

Each service gets the kind of load that shows what it is for: bursts that a
rate limiter must reject, a Zipf skewed set of links for a cache, retries
carrying the same idempotency key, duplicate notification ids, and one very
large account posting to a social feed. It uses only the standard library.

Run the services first, see observability/README.md, then this.
"""

import argparse
import collections
import json
import random
import threading
import time
import urllib.error
import urllib.request
import uuid

PORTS = {
    "rate-limiting": 9101,
    "url-shortener": 9102,
    "notification-platform": 9103,
    "social-feed": 9104,
    "payment-platform": 9105,
    "lab-server": 8081,
}


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None  # a 302 is the answer we want to count, not something to follow


_opener = urllib.request.build_opener(_NoRedirect)
_counts: collections.Counter = collections.Counter()
_lock = threading.Lock()


def call(service: str, method: str, url: str, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with _opener.open(req, timeout=5) as resp:
            status, text = resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        status, text = e.code, ""
    except Exception:
        status, text = 0, ""
    with _lock:
        _counts[(service, status)] += 1
    return status, text


def until(deadline: float):
    return time.time() < deadline


def rate_limiting(base: str, deadline: float, rng: random.Random):
    while until(deadline):
        client = f"client-{rng.randint(1, 3)}"
        for _ in range(rng.randint(5, 40)):  # a burst, which the limiter has to cut off
            call("rate-limiting", "GET", base + "/", headers={"X-Client-ID": client})
        time.sleep(rng.uniform(0.2, 0.8))


def url_shortener(base: str, deadline: float, rng: random.Random):
    codes = []
    for i in range(40):  # each link made by its own client, so the creation limit is not hit
        _, text = call("url-shortener", "POST", base + "/shorten", {"url": f"https://example.com/page/{i}"}, {"X-Client-ID": f"maker-{i}"})
        try:
            codes.append(json.loads(text)["code"])
        except Exception:
            pass
    weights = [1 / (i + 1) for i in range(len(codes))]  # Zipf: a few links get most of the traffic
    last_noise = 0.0
    while until(deadline) and codes:
        call("url-shortener", "GET", base + "/" + rng.choices(codes, weights)[0])
        if time.time() - last_noise > 5:  # one noisy client hammers creation, and gets rate limited
            last_noise = time.time()
            for j in range(40):
                call("url-shortener", "POST", base + "/shorten", {"url": f"https://example.com/spam/{j}"}, {"X-Client-ID": "noisy"})
        time.sleep(0.01)


def notifications(base: str, deadline: float, rng: random.Random):
    sent = []
    while until(deadline):
        roll = rng.random()
        if roll < 0.05:
            channel = "pigeon"  # no such channel, so it goes to the dead letter queue
        elif roll < 0.25:
            channel = "sms"  # rate limited downstream, some will exhaust their retries
        else:
            channel = rng.choice(["email", "push"])
        nid = rng.choice(sent) if sent and rng.random() < 0.1 else str(uuid.uuid4())  # now and then a duplicate id
        call("notification-platform", "POST", base + "/notifications",
             {"id": nid, "channel": channel, "recipient": "someone", "body": "hello", "priority": rng.choice(["low", "normal", "urgent"])})
        sent.append(nid)
        time.sleep(rng.uniform(0.002, 0.02))


def social_feed(base: str, deadline: float, rng: random.Random, celebrity_followers: int):
    users = [f"user{i}" for i in range(60)]
    for u in users:
        for followee in rng.sample(users, 5):
            if followee != u:
                call("social-feed", "POST", base + "/follow", {"follower": u, "followee": followee})
    for i in range(celebrity_followers):  # one account that everyone follows
        call("social-feed", "POST", base + "/follow", {"follower": f"fan{i}", "followee": "star"})
    next_star_post = time.time()
    while until(deadline):
        if time.time() >= next_star_post:  # four posts a second from an account with thousands of followers
            call("social-feed", "POST", base + "/posts", {"author": "star", "body": "big news"})
            next_star_post = time.time() + 0.25
        call("social-feed", "POST", base + "/posts", {"author": rng.choice(users), "body": "hi"})
        for _ in range(4):
            call("social-feed", "GET", base + f"/timeline/{rng.choice(users)}?limit=20")
        time.sleep(0.15)  # ordinary users post now and then, not constantly


def payments(base: str, deadline: float, rng: random.Random):
    keys = []
    while until(deadline):
        if keys and rng.random() < 0.3:
            key = rng.choice(keys)  # a client retrying a request it thinks failed
        else:
            key = str(uuid.uuid4())
            keys.append(key)
        call("payment-platform", "POST", base + "/payments", {"amount_cents": rng.randint(100, 20000), "currency": "usd"}, {"Idempotency-Key": key})
        time.sleep(rng.uniform(0.01, 0.06))


def lab_server(base: str, deadline: float, rng: random.Random):
    while until(deadline):
        call("lab-server", "GET", base + "/api/kafka/state")
        time.sleep(1)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--seconds", type=int, default=60)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--celebrity-followers", type=int, default=10000)
    ap.add_argument("--only", help="comma separated service names, default all")
    ap.add_argument("--seed", type=int, default=1)
    args = ap.parse_args()

    deadline = time.time() + args.seconds
    wanted = set(args.only.split(",")) if args.only else set(PORTS)
    jobs = {
        "rate-limiting": rate_limiting,
        "url-shortener": url_shortener,
        "notification-platform": notifications,
        "payment-platform": payments,
        "lab-server": lab_server,
    }
    threads = []
    for name, fn in jobs.items():
        if name in wanted:
            t = threading.Thread(target=fn, args=(f"http://{args.host}:{PORTS[name]}", deadline, random.Random(args.seed)), daemon=True)
            threads.append(t)
    if "social-feed" in wanted:
        threads.append(threading.Thread(target=social_feed, args=(f"http://{args.host}:{PORTS['social-feed']}", deadline, random.Random(args.seed), args.celebrity_followers), daemon=True))
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    print(f"{'service':24s} {'status':>6s} {'requests':>9s}")
    for (service, status), n in sorted(_counts.items()):
        print(f"{service:24s} {status or 'ERR':>6} {n:9d}")


if __name__ == "__main__":
    main()
