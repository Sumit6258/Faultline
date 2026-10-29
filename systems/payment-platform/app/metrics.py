"""Prometheus metrics for the payment platform.

The HTTP metrics use the same names and labels as the Go services in this
repository (http_requests_total, http_request_duration_seconds,
http_requests_in_flight), so one dashboard and one set of alert rules cover
every service whatever language it is written in.

The route label is the route template, "/payments/{idempotency_key}", never
the URL. An idempotency key is a fresh value on every request, and one time
series per key would be exactly the cardinality explosion the Go kit's series
cap exists to stop.

The outbox numbers are read from the database when Prometheus scrapes, over
a connection of their own, following the one rule in app/db.py: a connection
belongs to one thread of work.
"""

import sqlite3
import time

from prometheus_client import (
    CONTENT_TYPE_LATEST,
    CollectorRegistry,
    Counter,
    Gauge,
    GCCollector,
    Histogram,
    PlatformCollector,
    ProcessCollector,
    disable_created_metrics,
    generate_latest,
)
from prometheus_client.core import CounterMetricFamily, GaugeMetricFamily
from starlette.requests import Request
from starlette.responses import Response

from app.db import open_connection

# The Go services do not publish the "_created" timestamp series, so neither does this.
disable_created_metrics()

# The same buckets as the Go kit's DefBuckets, so latency quantiles compare across services.
BUCKETS = (0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5)

KNOWN_METHODS = {"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}


class OutboxCollector:
    """Publishes the state of the outbox, read fresh on every scrape.

    A stalled relay is the failure the outbox pattern is most exposed to:
    payments keep committing, their events pile up, and nothing downstream
    hears about them. The age of the oldest unpublished event is the number
    that shows it.
    """

    def __init__(self, db_path: str, errors: Counter):
        self.db_path = db_path
        self.errors = errors

    def collect(self):
        try:
            conn = open_connection(self.db_path)
            try:
                pending, age = conn.execute(
                    "SELECT COUNT(*), CAST((julianday('now') - julianday(MIN(created_at))) * 86400 AS REAL) "
                    "FROM outbox WHERE dispatched_at IS NULL"
                ).fetchone()
                dispatched = conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NOT NULL").fetchone()[0]
            finally:
                conn.close()
        except sqlite3.Error:
            # One unreadable database must not take the whole metrics page down.
            self.errors.labels("outbox").inc()
            return

        yield GaugeMetricFamily("payments_outbox_pending", "Events written but not yet published by the relay.", value=pending)
        if pending == 0:
            age = 0.0
        elif age is None:
            age = float("nan")  # events exist but their timestamps cannot be read: say so, do not guess
        yield GaugeMetricFamily(
            "payments_outbox_oldest_pending_age_seconds",
            "Age of the oldest event the relay has not published yet. Zero when nothing is waiting.",
            value=age,
        )
        yield CounterMetricFamily("payments_outbox_dispatched", "Events the relay has published.", value=dispatched)


class Metrics:
    def __init__(self, db_path: str):
        self.registry = CollectorRegistry()
        ProcessCollector(registry=self.registry)
        PlatformCollector(registry=self.registry)
        GCCollector(registry=self.registry)

        self.requests = Counter(
            "http_requests", "HTTP requests handled, by method, matched route and status code.",
            ["method", "route", "status"], registry=self.registry,
        )
        self.duration = Histogram(
            "http_request_duration_seconds", "HTTP request latency in seconds, by method and matched route.",
            ["method", "route"], buckets=BUCKETS, registry=self.registry,
        )
        self.in_flight = Gauge("http_requests_in_flight", "HTTP requests currently being handled.", registry=self.registry)

        self.payments_created = Counter("payments_created", "Payments newly created.", registry=self.registry)
        self.payments_deduplicated = Counter(
            "payments_deduplicated", "Retries answered from an existing payment instead of creating a second one.",
            registry=self.registry,
        )
        self.collector_errors = Counter(
            "obs_collector_errors", "Scrape time collectors that failed and were skipped.", ["collector"], registry=None,
        )
        self.registry.register(OutboxCollector(db_path, self.collector_errors))
        # Registered after the collectors that increment it, so a failure shows
        # on the very scrape it happened in instead of one scrape later.
        self.registry.register(self.collector_errors)


def install(app, db_path: str) -> Metrics:
    """Adds request metrics and a /metrics endpoint to a FastAPI app."""
    metrics = Metrics(db_path)

    @app.middleware("http")
    async def record_request(request: Request, call_next):
        if request.url.path == "/metrics":
            return await call_next(request)  # scrapes are not traffic
        method = request.method if request.method in KNOWN_METHODS else "OTHER"
        metrics.in_flight.inc()
        start = time.perf_counter()
        status = 500  # what an unhandled exception becomes
        try:
            response = await call_next(request)
            status = response.status_code
            return response
        finally:
            route = getattr(request.scope.get("route"), "path", None) or "unmatched"
            metrics.in_flight.dec()
            metrics.duration.labels(method, route).observe(time.perf_counter() - start)
            metrics.requests.labels(method, route, str(status)).inc()

    @app.get("/metrics", include_in_schema=False)
    def scrape():
        return Response(generate_latest(metrics.registry), media_type=CONTENT_TYPE_LATEST)

    return metrics
