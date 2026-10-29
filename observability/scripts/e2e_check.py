#!/usr/bin/env python3
"""End to end checks against a Prometheus that is scraping the live services.

Unit tests prove each piece on its own. This proves they agree with each
other: that every service's metrics page is accepted by Prometheus, that
every dashboard query runs and finds data, and that the alerts fire for
real conditions and stay quiet otherwise.

    e2e_check.py lint        run promtool's metrics linter over each service
    e2e_check.py dashboards  every dashboard query runs and finds data, with all services up
    e2e_check.py alerts      after a service is stopped, real conditions raise alerts and only those

scripts/e2e.sh starts everything and calls this.
"""

import argparse
import json
import pathlib
import subprocess
import sys
import time
import urllib.parse
import urllib.request

OBS = pathlib.Path(__file__).resolve().parents[1]
PORTS = {"rate-limiting": 9101, "url-shortener": 9102, "notification-platform": 9103,
         "social-feed": 9104, "payment-platform": 9105, "lab-server": 8081}

# Alerts that real conditions in the demo traffic must raise, and alerts that must stay quiet.
MUST_FIRE = ["KafkaConsumerLagGrowing", "SocialFeedFanoutAmplificationHigh", "NotificationDeadLetterQueueGrowing", "InstanceDown"]
MUST_BE_QUIET = ["PaymentOutboxStuck", "HighErrorRatio", "HighLatencyP99", "NotificationQueueBacklog", "URLShortenerCacheHitRatioLow"]

# Dashboard panels that legitimately show nothing under the demo traffic, and why.
MAY_BE_EMPTY = {
    "5xx ratio": "the demo traffic produces no server errors, so there is no numerator",
}


def get(url: str) -> str:
    with urllib.request.urlopen(url, timeout=10) as r:
        return r.read().decode()


def query(prom: str, expr: str):
    body = json.loads(get(f"{prom}/api/v1/query?" + urllib.parse.urlencode({"query": expr})))
    if body["status"] != "success":
        raise RuntimeError(body.get("error", "query failed"))
    return body["data"]["result"]


def lint(args) -> int:
    failed = 0
    for job, port in PORTS.items():
        try:
            text = get(f"http://127.0.0.1:{port}/metrics")
        except Exception as e:
            print(f"  FAIL {job:24s} /metrics could not be fetched: {e}")
            failed += 1
            continue
        r = subprocess.run([args.promtool, "check", "metrics"], input=text, text=True, capture_output=True)
        families = text.count("# TYPE ")
        print(f"  {'ok  ' if r.returncode == 0 else 'FAIL'} {job:24s} {families} metric families" + ("" if r.returncode == 0 else "\n" + r.stderr + r.stdout))
        failed += r.returncode != 0
    return failed


def dashboard_queries():
    for f in sorted((OBS / "grafana" / "dashboards").glob("*.json")):
        for panel in json.loads(f.read_text())["panels"]:
            for t in panel.get("targets", []):
                yield f.stem, panel["title"], t["expr"]


class Checker:
    def __init__(self):
        self.failed = 0

    def __call__(self, ok: bool, label: str, detail: str = ""):
        print(f"  {'ok  ' if ok else 'FAIL'} {label}" + (f"  ({detail})" if detail else ""))
        self.failed += not ok


def target_health(prom: str, want_up, wait: int = 30) -> dict:
    deadline = time.time() + wait
    while True:
        targets = json.loads(get(f"{prom}/api/v1/targets"))["data"]["activeTargets"]
        health = {t["labels"]["job"]: t["health"] for t in targets}
        if all(health.get(j) == "up" for j in want_up) or time.time() > deadline:
            return health
        time.sleep(1)


def dashboards(args) -> int:
    """Everything is up. Every dashboard query must run and find data."""
    prom, check = args.prometheus, Checker()

    print("Scrape targets")
    health = target_health(prom, PORTS)
    for job in PORTS:
        check(health.get(job) == "up", f"{job} is being scraped", health.get(job, "missing"))

    print("\nRecording rules")
    series = {r["metric"]["job"] for r in query(prom, "job:http_requests:rate5m")}
    check(set(PORTS) <= series, "job:http_requests:rate5m has a series for every service", ", ".join(sorted(series)))
    check(len(query(prom, "job_route:http_request_duration_seconds:p99_5m")) > 0, "the p99 recording rule produces series")

    print("\nDashboard queries")
    for board, title, expr in dashboard_queries():
        q = expr.replace("$__rate_interval", args.rate_interval).replace("$job", ".+")
        try:
            result = query(prom, q)
        except Exception as e:
            check(False, f"{board}: {title}", f"query failed: {e}")
            continue
        if result:
            value = float(result[0]["value"][1])
            check(True, f"{board}: {title}", f"{len(result)} series, first value {value:.4g}")
        elif title in MAY_BE_EMPTY:
            check(True, f"{board}: {title}", "empty, allowed: " + MAY_BE_EMPTY[title])
        else:
            check(False, f"{board}: {title}", "no data")
    return check.failed


def alerts(args) -> int:
    """A service has been stopped on purpose. Real conditions must raise alerts, and only those."""
    prom, check = args.prometheus, Checker()

    print("Scrape targets")
    health = target_health(prom, [j for j in PORTS if j != args.expect_down])
    for job in PORTS:
        if job == args.expect_down:
            check(health.get(job) == "down", f"{job} is down, as the script made it", health.get(job, "missing"))
        else:
            check(health.get(job) == "up", f"{job} is being scraped", health.get(job, "missing"))

    print("\nAlerts")
    deadline = time.time() + args.wait
    while True:
        active = json.loads(get(f"{prom}/api/v1/alerts"))["data"]["alerts"]
        firing = {a["labels"]["alertname"] for a in active if a["state"] == "firing"}
        if all(a in firing for a in MUST_FIRE) or time.time() > deadline:
            break
        time.sleep(1)
    for name in MUST_FIRE:
        check(name in firing, f"{name} fires for the real condition")
    down = [a["labels"].get("job", "?") for a in active if a["labels"]["alertname"] == "InstanceDown" and a["state"] == "firing"]
    check(down == [args.expect_down], f"InstanceDown names exactly the service that was stopped", ", ".join(down))
    for name in MUST_BE_QUIET:
        check(name not in firing, f"{name} stays quiet while its condition is absent")
    print(f"\nfiring now: {', '.join(sorted(firing)) or 'nothing'}")
    return check.failed


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    l = sub.add_parser("lint")
    l.add_argument("--promtool", default="promtool")
    d = sub.add_parser("dashboards")
    d.add_argument("--prometheus", default="http://127.0.0.1:9090")
    d.add_argument("--rate-interval", default="15s", help="stands in for Grafana's $__rate_interval")
    a = sub.add_parser("alerts")
    a.add_argument("--prometheus", default="http://127.0.0.1:9090")
    a.add_argument("--expect-down", default="rate-limiting", help="the service e2e.sh stopped on purpose")
    a.add_argument("--wait", type=int, default=40, help="seconds to wait for alerts to fire")
    args = ap.parse_args()
    failed = {"lint": lint, "dashboards": dashboards, "alerts": alerts}[args.cmd](args)
    print(f"\n{'FAILED: ' + str(failed) + ' check(s)' if failed else 'all checks passed'}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
