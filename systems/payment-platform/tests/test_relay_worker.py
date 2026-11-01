import importlib
import json
import os
import signal
import subprocess
import sys
import time
import uuid
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app import relay_worker
from app.db import connect, open_connection
from app.outbox_relay import run_once
from app.payments import create_payment

APP_DIR = Path(__file__).resolve().parents[1]


@pytest.fixture
def db_path(tmp_path):
    path = str(tmp_path / "relay.db")
    connect(path).close()
    return path


def make_payments(db_path: str, n: int) -> list[str]:
    conn = open_connection(db_path)
    keys = [str(uuid.uuid4()) for _ in range(n)]
    for k in keys:
        create_payment(conn, k, 100, "usd")
    conn.close()
    return keys


def sink_keys(path) -> list[str]:
    return [json.loads(line)["idempotency_key"] for line in Path(path).read_text().splitlines()]


def dispatched_count(db_path: str) -> int:
    conn = open_connection(db_path)
    try:
        return conn.execute("SELECT COUNT(*) FROM outbox WHERE dispatched_at IS NOT NULL").fetchone()[0]
    finally:
        conn.close()


def test_once_publishes_every_waiting_event_exactly_once_and_marks_them(db_path, tmp_path):
    keys = make_payments(db_path, 25)
    sink = tmp_path / "sink.jsonl"
    assert relay_worker.main(["--db", db_path, "--sink", str(sink), "--once"]) == 0
    assert sorted(sink_keys(sink)) == sorted(keys)
    assert dispatched_count(db_path) == 25
    # nothing left, so a second pass publishes nothing new
    relay_worker.main(["--db", db_path, "--sink", str(sink), "--once"])
    assert len(sink_keys(sink)) == 25


def test_a_crash_between_publishing_and_marking_publishes_that_event_again_never_zero_times(db_path, tmp_path):
    """The at-least-once guarantee, deterministically: die after the third event reaches the sink but before it is marked."""
    keys = make_payments(db_path, 6)
    sink_path = tmp_path / "sink.jsonl"
    conn = open_connection(db_path)

    with open(sink_path, "a", buffering=1) as sink:
        real = relay_worker.make_publisher(sink, 0)
        seen = []

        def dies_on_the_third(event_type, payload):
            real(event_type, payload)  # it reaches the broker...
            seen.append(1)
            if len(seen) == 3:
                raise RuntimeError("killed before the mark")  # ...but the process dies before marking it dispatched

        with pytest.raises(RuntimeError):
            run_once(conn, publish=dies_on_the_third, now_fn=relay_worker._now)
        assert dispatched_count(db_path) == 2

        # restart
        run_once(conn, publish=real, now_fn=relay_worker._now)
    conn.close()

    published = sink_keys(sink_path)
    assert set(published) == set(keys), "an event was never published"
    assert len(published) == 7, "exactly one event, the one in flight at the crash, is published twice"
    assert dispatched_count(db_path) == 6


def test_publish_delay_widens_the_window(db_path, tmp_path):
    make_payments(db_path, 5)
    start = time.perf_counter()
    relay_worker.main(["--db", db_path, "--sink", str(tmp_path / "s.jsonl"), "--once", "--publish-delay-ms", "40"])
    assert time.perf_counter() - start >= 5 * 0.04


def test_sigterm_stops_between_events_so_nothing_is_published_without_being_marked(db_path, tmp_path):
    make_payments(db_path, 200)
    sink = tmp_path / "sink.jsonl"
    proc = subprocess.Popen(
        [sys.executable, "-m", "app.relay_worker", "--db", db_path, "--sink", str(sink), "--publish-delay-ms", "20"],
        cwd=APP_DIR,
    )
    deadline = time.time() + 10
    while not sink.exists() or not sink.read_text().count("\n") >= 5:
        assert time.time() < deadline and proc.poll() is None, "the worker never started publishing"
        time.sleep(0.02)
    asked = time.perf_counter()
    proc.send_signal(signal.SIGTERM)
    assert proc.wait(timeout=10) == 0
    assert time.perf_counter() - asked < 1.0, "it should stop between events, not finish the whole batch first"

    published, dispatched = len(sink_keys(sink)), dispatched_count(db_path)
    assert 0 < dispatched < 200, "it should have stopped part way"
    assert published == dispatched, "a graceful stop must not leave an event published but not marked"


class TestApiRelayMode:
    @pytest.fixture
    def reload_main(self, tmp_path, monkeypatch):
        monkeypatch.setenv("PAYMENT_DB_PATH", str(tmp_path / "app.db"))
        loaded = []

        def load(mode):
            if mode is None:
                monkeypatch.delenv("PAYMENT_RELAY", raising=False)
            else:
                monkeypatch.setenv("PAYMENT_RELAY", mode)
            import app.main as main

            m = importlib.reload(main)
            loaded.append(m)
            return m

        yield load
        for m in loaded:
            m._stop_relay.set()

    def test_the_relay_runs_inside_the_api_by_default(self, reload_main):
        assert reload_main(None)._relay_thread.is_alive()

    def test_external_mode_starts_no_relay_thread(self, reload_main):
        assert reload_main("external")._relay_thread is None

    def test_a_mistyped_mode_fails_at_startup_instead_of_silently_running_no_relay(self, reload_main):
        with pytest.raises(ValueError, match="PAYMENT_RELAY"):
            reload_main("extrenal")
