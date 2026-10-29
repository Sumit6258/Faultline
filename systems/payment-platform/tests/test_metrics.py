import importlib
import math
import sys
import uuid
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from fastapi import FastAPI
from fastapi.testclient import TestClient
from prometheus_client import generate_latest
from prometheus_client.parser import text_string_to_metric_families

from app.db import connect, open_connection
from app.metrics import BUCKETS, Metrics, install
from app.outbox_relay import run_once
from app.payments import create_payment


def parse(text: str) -> dict:
    """Reads a metrics page with the official Prometheus parser.

    Returns {(sample name, frozenset of labels): value}, so these tests check
    what a real scraper would see rather than matching strings.
    """
    out = {}
    for family in text_string_to_metric_families(text):
        for s in family.samples:
            out[(s.name, frozenset(s.labels.items()))] = s.value
    return out


def value(samples: dict, name: str, **labels) -> float:
    return samples[(name, frozenset(labels.items()))]


def scrape(metrics: Metrics) -> dict:
    return parse(generate_latest(metrics.registry).decode())


@pytest.fixture
def db_path(tmp_path):
    path = str(tmp_path / "metrics.db")
    connect(path).close()
    return path


class TestOutboxMetrics:
    def test_pending_age_and_dispatched_follow_the_outbox(self, db_path):
        conn = open_connection(db_path)
        metrics = Metrics(db_path)
        for _ in range(3):
            create_payment(conn, str(uuid.uuid4()), 100, "usd")

        s = scrape(metrics)
        assert value(s, "payments_outbox_pending") == 3
        assert value(s, "payments_outbox_dispatched_total") == 0
        assert 0 <= value(s, "payments_outbox_oldest_pending_age_seconds") < 60

        published = []
        while run_once(conn, publish=lambda t, p: published.append(t), now_fn=lambda: "2026-01-01T00:00:00Z"):
            pass
        s = scrape(metrics)
        assert value(s, "payments_outbox_pending") == 0
        assert value(s, "payments_outbox_dispatched_total") == 3
        assert value(s, "payments_outbox_oldest_pending_age_seconds") == 0
        conn.close()

    def test_the_age_is_that_of_the_oldest_waiting_event(self, db_path):
        conn = open_connection(db_path)
        with conn:
            conn.execute("INSERT INTO outbox (event_type, payload, created_at) VALUES ('old', '{}', '2020-01-01T00:00:00Z')")
        create_payment(conn, str(uuid.uuid4()), 100, "usd")  # a fresh one alongside it

        age = value(scrape(Metrics(db_path)), "payments_outbox_oldest_pending_age_seconds")
        assert age > 5 * 365 * 86400, "the timestamp of the oldest event, written with a trailing Z, must be read as UTC"
        conn.close()

    def test_unreadable_timestamps_show_as_nan_not_as_a_healthy_zero(self, db_path):
        conn = open_connection(db_path)
        with conn:
            conn.execute("INSERT INTO outbox (event_type, payload, created_at) VALUES ('x', '{}', 'not a time')")
        s = scrape(Metrics(db_path))
        assert value(s, "payments_outbox_pending") == 1
        assert math.isnan(value(s, "payments_outbox_oldest_pending_age_seconds"))
        conn.close()

    def test_an_unreadable_database_is_counted_and_does_not_break_the_page(self, tmp_path):
        metrics = Metrics(str(tmp_path / "no-such-dir" / "x.db"))
        s = scrape(metrics)
        assert not any(name == "payments_outbox_pending" for name, _ in s)
        assert value(s, "obs_collector_errors_total", collector="outbox") == 1
        s = scrape(metrics)
        assert value(s, "obs_collector_errors_total", collector="outbox") == 2


@pytest.fixture
def small_app(db_path):
    app = FastAPI()
    metrics = install(app, db_path)

    @app.get("/items/{item_id}")
    def item(item_id: str):
        return {"id": item_id}

    @app.get("/boom")
    def boom():
        raise RuntimeError("handler bug")

    return app, metrics


class TestHttpMetrics:
    def test_requests_are_labeled_by_route_template_not_by_url(self, small_app):
        app, metrics = small_app
        client = TestClient(app, raise_server_exceptions=False)
        client.get("/items/alice")
        client.get("/items/bob")
        client.get("/nowhere")

        s = scrape(metrics)
        assert value(s, "http_requests_total", method="GET", route="/items/{item_id}", status="200") == 2
        assert value(s, "http_requests_total", method="GET", route="unmatched", status="404") == 1
        assert not any("alice" in str(labels) or "bob" in str(labels) for _, labels in s), "an id leaked into a label"
        assert value(s, "http_request_duration_seconds_count", method="GET", route="/items/{item_id}") == 2

    def test_a_crashing_handler_is_counted_as_a_500(self, small_app):
        app, metrics = small_app
        TestClient(app, raise_server_exceptions=False).get("/boom")
        s = scrape(metrics)
        assert value(s, "http_requests_total", method="GET", route="/boom", status="500") == 1
        assert value(s, "http_requests_in_flight") == 0

    def test_client_chosen_methods_share_one_label_value(self, small_app):
        app, metrics = small_app
        client = TestClient(app, raise_server_exceptions=False)
        for method in ("FOO", "BAR"):
            client.request(method, "/items/x")
        s = scrape(metrics)
        assert not any("FOO" in str(labels) or "BAR" in str(labels) for _, labels in s)
        assert sum(v for (n, labels), v in s.items() if n == "http_requests_total" and ("method", "OTHER") in labels) == 2

    def test_scrapes_are_not_counted_as_traffic(self, small_app):
        app, metrics = small_app
        client = TestClient(app)
        client.get("/metrics")
        client.get("/metrics")
        assert not any(("route", "/metrics") in labels for _, labels in scrape(metrics))

    def test_the_endpoint_serves_the_text_format(self, small_app):
        app, _ = small_app
        r = TestClient(app).get("/metrics")
        assert r.status_code == 200
        assert r.headers["content-type"].startswith("text/plain; version=")
        assert "# TYPE http_requests_total counter" in r.text


class TestParityWithTheGoServices:
    """One dashboard and one set of alert rules serve every service, so the
    names, the labels and the latency buckets have to match the Go kit."""

    def test_http_metric_names_and_labels(self, small_app):
        app, metrics = small_app
        TestClient(app).get("/items/a")
        s = scrape(metrics)
        names = {n for n, _ in s}
        assert {"http_requests_total", "http_request_duration_seconds_bucket", "http_request_duration_seconds_sum",
                "http_request_duration_seconds_count", "http_requests_in_flight"} <= names
        req_labels = {k for (n, labels) in s for k, _ in labels if n == "http_requests_total"}
        assert req_labels == {"method", "route", "status"}
        assert not any(n.endswith("_created") for n in names), "the Go services publish no _created series"

    def test_latency_buckets_match_the_go_kit(self, small_app):
        app, metrics = small_app
        TestClient(app).get("/items/a")
        le = sorted(
            float(dict(labels)["le"]) for (n, labels) in scrape(metrics)
            if n == "http_request_duration_seconds_bucket" and dict(labels)["route"] == "/items/{item_id}" and dict(labels)["le"] != "+Inf"
        )
        go_default_buckets = [0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5]
        assert le == go_default_buckets
        assert list(BUCKETS) == go_default_buckets


@pytest.fixture
def main_module(tmp_path, monkeypatch):
    monkeypatch.setenv("PAYMENT_DB_PATH", str(tmp_path / "app.db"))
    import app.main as main

    main = importlib.reload(main)
    yield main
    main._stop_relay.set()


class TestTheRealApp:
    def test_created_and_deduplicated_payments_are_counted_separately(self, main_module):
        client = TestClient(main_module.app)
        key = str(uuid.uuid4())
        body = {"amount_cents": 500, "currency": "usd"}
        assert client.post("/payments", json=body, headers={"Idempotency-Key": key}).json()["created"] is True
        assert client.post("/payments", json=body, headers={"Idempotency-Key": key}).json()["created"] is False
        assert client.post("/payments", json=body, headers={"Idempotency-Key": key}).json()["created"] is False
        for _ in range(2):  # two more distinct payments, so the two counts differ and a swap cannot hide
            client.post("/payments", json=body, headers={"Idempotency-Key": str(uuid.uuid4())})
        client.get(f"/payments/{key}")

        s = parse(client.get("/metrics").text)
        assert value(s, "payments_created_total") == 3
        assert value(s, "payments_deduplicated_total") == 2
        assert value(s, "http_requests_total", method="POST", route="/payments", status="200") == 5
        assert value(s, "http_requests_total", method="GET", route="/payments/{idempotency_key}", status="200") == 1
