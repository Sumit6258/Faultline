#!/usr/bin/env python3
"""Generates the Grafana dashboards in dashboards/.

The JSON is generated, not written by hand, so a panel cannot drift from the
metric it shows, and so the same helpers keep every panel consistent. Edit
this file and run it. CI runs it with --check and fails if the committed JSON
differs from what this produces.

    python3 build_dashboards.py          write dashboards/*.json
    python3 build_dashboards.py --check  exit 1 if they are out of date

Expressions use two Grafana variables: $__rate_interval, which Grafana
replaces with a window suited to the scrape interval, and $job, the service
selector at the top of the page.
"""

import json
import pathlib
import sys

DS = {"type": "prometheus", "uid": "prometheus"}
OUT = pathlib.Path(__file__).parent / "dashboards"
R = "$__rate_interval"
J = 'job=~"$job"'


class Board:
    def __init__(self, uid: str, title: str, description: str, with_job_variable: bool):
        self.uid, self.title, self.description = uid, title, description
        self.panels: list[dict] = []
        self.next_id = 1
        self.y = 0
        self.col = 0
        self.with_job_variable = with_job_variable

    def _id(self) -> int:
        i = self.next_id
        self.next_id += 1
        return i

    def row(self, title: str):
        if self.col:
            self.col, self.y = 0, self.y + 8
        self.panels.append({"id": self._id(), "type": "row", "title": title, "collapsed": False, "panels": [],
                            "gridPos": {"h": 1, "w": 24, "x": 0, "y": self.y}})
        self.y += 1

    def _place(self, w: int, h: int) -> dict:
        if self.col + w > 24:
            self.col, self.y = 0, self.y + 8
        pos = {"h": h, "w": w, "x": self.col, "y": self.y}
        self.col += w
        if self.col >= 24:
            self.col, self.y = 0, self.y + h
        return pos

    def timeseries(self, title: str, targets: list[tuple[str, str]], unit: str = "short", description: str = "",
                   w: int = 12, h: int = 8, threshold: float | None = None, stack: bool = False):
        defaults: dict = {"unit": unit, "custom": {"lineWidth": 1, "fillOpacity": 12, "showPoints": "never"}}
        if stack:
            defaults["custom"]["stacking"] = {"mode": "normal"}
        if threshold is not None:
            defaults["thresholds"] = {"mode": "absolute", "steps": [{"color": "green", "value": None}, {"color": "red", "value": threshold}]}
            defaults["custom"]["thresholdsStyle"] = {"mode": "line"}
        self.panels.append({
            "id": self._id(), "type": "timeseries", "title": title, "description": description, "datasource": DS,
            "gridPos": self._place(w, h),
            "fieldConfig": {"defaults": defaults, "overrides": []},
            "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "multi", "sort": "desc"}},
            "targets": [{"refId": chr(65 + i), "datasource": DS, "expr": expr, "legendFormat": legend}
                        for i, (expr, legend) in enumerate(targets)],
        })

    def build(self) -> dict:
        templating = {"list": []}
        if self.with_job_variable:
            templating["list"].append({
                "name": "job", "label": "service", "type": "query", "datasource": DS,
                "query": {"query": "label_values(up, job)", "refId": "job"}, "definition": "label_values(up, job)",
                "refresh": 2, "multi": True, "includeAll": True, "sort": 1,
                "current": {"selected": True, "text": ["All"], "value": ["$__all"]},
            })
        return {
            "uid": self.uid, "title": self.title, "description": self.description, "tags": ["faultline"],
            "schemaVersion": 39, "version": 1, "editable": True, "graphTooltip": 1, "timezone": "browser",
            "time": {"from": "now-15m", "to": "now"}, "refresh": "5s",
            "templating": templating, "annotations": {"list": []}, "links": [], "panels": self.panels,
        }


def red_board() -> dict:
    b = Board("faultline-red", "Faultline: service health", "Request rate, errors and latency for every service, from the shared metrics kit.", True)
    b.row("Rate, errors, duration")
    b.timeseries("Requests per second", [(f"sum by (job) (rate(http_requests_total{{{J}}}[{R}]))", "{{job}}")], "reqps",
                 "Traffic by service. The /metrics endpoint is excluded, so scrapes do not count as traffic.")
    b.timeseries("5xx ratio", [(f'sum by (job) (rate(http_requests_total{{{J}, status=~"5.."}}[{R}])) / sum by (job) (rate(http_requests_total{{{J}}}[{R}]))', "{{job}}")],
                 "percentunit", "Share of requests that ended in a server error. The HighErrorRatio alert fires above 5%.", threshold=0.05)
    b.timeseries("p99 latency by route", [(f"histogram_quantile(0.99, sum by (job, route, le) (rate(http_request_duration_seconds_bucket{{{J}}}[{R}])))", "{{job}} {{route}}")],
                 "s", "99th percentile. Long lived event streams are left out of the histogram on purpose.", threshold=0.25)
    b.timeseries("Requests by status", [(f"sum by (job, status) (rate(http_requests_total{{{J}}}[{R}]))", "{{job}} {{status}}")], "reqps",
                 "Includes 429s from rate limiters, which are the system working, not failing.", stack=True)
    b.timeseries("Requests in flight", [(f"sum by (job) (http_requests_in_flight{{{J}}})", "{{job}}")], "short")
    b.timeseries("Scrape targets up", [(f"up{{{J}}}", "{{job}}")], "short", "1 is up, 0 is down. The InstanceDown alert fires after a minute at 0.")
    b.row("Process")
    b.timeseries("Goroutines", [(f"go_goroutines{{{J}}}", "{{job}}")], "short", "Go services only. A count that only ever climbs is a leak.")
    b.timeseries("Heap in use", [(f"go_memstats_alloc_bytes{{{J}}}", "{{job}}")], "bytes", "Go services only.")
    return b.build()


def systems_board() -> dict:
    b = Board("faultline-systems", "Faultline: what each system is doing",
              "The numbers specific to each system, each tied to a failure mode the repository demonstrates.", False)

    b.row("Payment platform: the outbox")
    b.timeseries("Payments created vs deduplicated", [
        ("rate(payments_created_total[$__rate_interval])", "created"),
        ("rate(payments_deduplicated_total[$__rate_interval])", "deduplicated retries"),
    ], "ops", "A retry answered from an existing payment is a charge that did not happen twice.")
    b.timeseries("Events waiting in the outbox", [("payments_outbox_pending", "pending")], "short",
                 "Committed payments whose event has not been published yet. Normally close to zero.")
    b.timeseries("Age of the oldest unpublished event", [("payments_outbox_oldest_pending_age_seconds", "oldest")], "s",
                 "The stalled relay detector. The PaymentOutboxStuck alert fires above 30 seconds.", threshold=30)
    b.timeseries("Events published", [("rate(payments_outbox_dispatched_total[$__rate_interval])", "published")], "ops")

    b.row("Notification platform: the delivery pipeline")
    b.timeseries("Delivered per second", [("rate(notification_delivered_total[$__rate_interval])", "delivered")], "ops")
    b.timeseries("Queue depth", [("notification_queue_depth", "waiting")], "short",
                 "Ready or scheduled. The NotificationQueueBacklog alert fires above 1000.", threshold=1000)
    b.timeseries("Dead letter queue size", [("notification_dead_letter_queue_size", "dead lettered")], "short",
                 "Notifications that gave up. It only grows, so watch its slope.")

    b.row("Social feed: the celebrity problem")
    b.timeseries("Inbox writes per post", [(
        'sum(rate(socialfeed_inbox_writes_total[$__rate_interval])) / sum(rate(http_requests_total{job="social-feed", route="/posts", method="POST", status="200"}[$__rate_interval]))',
        "writes per post")], "short",
        "Fan out amplification. Near the average follower count under fan out on write, near zero under fan out on read.", threshold=1000)
    b.timeseries("Inbox writes per second", [("rate(socialfeed_inbox_writes_total[$__rate_interval])", "inbox writes")], "ops")
    b.timeseries("Timeline read latency (p99)", [(
        'histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{job="social-feed", route="/timeline/"}[$__rate_interval])))',
        "p99")], "s", "Fan out on read pays here, on every refresh.")
    b.timeseries("Strategy in use", [("socialfeed_strategy_info", "{{strategy}}")], "short", "Which timeline strategy this run is using.")

    b.row("URL shortener: the cache")
    b.timeseries("Cache hit ratio", [(
        "sum(rate(urlshortener_cache_hits_total[$__rate_interval])) / (sum(rate(urlshortener_cache_hits_total[$__rate_interval])) + sum(rate(urlshortener_cache_misses_total[$__rate_interval])))",
        "hit ratio")], "percentunit", "Zipf traffic against a big enough cache sits above 90%.")
    b.timeseries("Link creations: accepted vs rate limited", [
        ('sum(rate(http_requests_total{job="url-shortener", route="/shorten", status!="429"}[$__rate_interval]))', "accepted"),
        ('sum(rate(http_requests_total{job="url-shortener", route="/shorten", status="429"}[$__rate_interval]))', "rate limited"),
    ], "reqps")

    b.row("Rate limiting lab")
    b.timeseries("Allowed vs rejected", [
        ('sum(rate(http_requests_total{job="rate-limiting", status!="429"}[$__rate_interval]))', "allowed"),
        ('sum(rate(http_requests_total{job="rate-limiting", status="429"}[$__rate_interval]))', "rejected"),
    ], "reqps")
    b.timeseries("Algorithm in use", [("ratelimit_algorithm_info", "{{algorithm}}")], "short")

    b.row("Kafka lab (live): consumer lag")
    b.timeseries("Total consumer lag", [("labserver_kafka_total_lag", "total lag")], "short",
                 "The KafkaConsumerLagGrowing alert fires when this is above 100 and still climbing.")
    b.timeseries("Lag by partition", [("labserver_kafka_partition_lag", "partition {{partition}}")], "short",
                 "A hot key puts all of it on one partition, however many consumers there are.", stack=True)
    b.timeseries("Produced vs consumed", [
        ("rate(labserver_kafka_produced_total[$__rate_interval])", "produced"),
        ("rate(labserver_kafka_consumed_total[$__rate_interval])", "consumed"),
    ], "ops", "Lag grows whenever the first line is above the second.")
    b.timeseries("Browser tabs on the live page", [("labserver_stream_subscribers", "subscribers")], "short")
    return b.build()


BOARDS = {"faultline-red.json": red_board, "faultline-systems.json": systems_board}


def render(board: dict) -> str:
    return json.dumps(board, indent=2) + "\n"


def main() -> int:
    stale = []
    for name, build in BOARDS.items():
        text = render(build())
        path = OUT / name
        if "--check" in sys.argv:
            if not path.exists() or path.read_text() != text:
                stale.append(name)
        else:
            OUT.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
            print("wrote", path.relative_to(pathlib.Path(__file__).parent))
    if stale:
        print("out of date, run build_dashboards.py:", ", ".join(stale))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
