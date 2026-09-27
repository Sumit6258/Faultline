import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import pytest

from app.db import connect
from app.payments import create_payment
from app.outbox_relay import run_once, fetch_undispatched


@pytest.fixture
def conn(tmp_path):
    c = connect(str(tmp_path / "test.db"))
    yield c
    c.close()


def test_run_once_dispatches_all_pending(conn):
    for _ in range(5):
        create_payment(conn, str(uuid.uuid4()), 1000, "usd")

    published = []
    dispatched = run_once(conn, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "now")

    assert dispatched == 5
    assert len(published) == 5
    assert fetch_undispatched(conn) == []


def test_dispatched_events_are_not_redispatched(conn):
    create_payment(conn, str(uuid.uuid4()), 1000, "usd")

    published = []
    run_once(conn, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "now")
    run_once(conn, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "now")

    assert len(published) == 1, "the second run_once should find nothing left to dispatch"


def test_crash_mid_batch_then_resume_dispatches_remainder_not_duplicates(conn):
    for _ in range(5):
        create_payment(conn, str(uuid.uuid4()), 1000, "usd")

    published = []

    with pytest.raises(RuntimeError):
        run_once(conn, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "now", fail_after=3)

    assert len(published) == 3, "expected exactly 3 dispatches before the simulated crash"
    assert len(fetch_undispatched(conn)) == 2, "the other 2 events should still be pending, not lost"

    # The relay "restarts" and resumes.
    resumed = run_once(conn, publish=lambda t, p: published.append((t, p)), now_fn=lambda: "now")

    assert resumed == 2, "expected the resumed run to pick up exactly the 2 that were left"
    assert len(published) == 5, "5 total dispatches across both runs, none lost, none duplicated"
    assert fetch_undispatched(conn) == []
