import http.server
import sys
import threading
import time
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[3]))

from chaos.harness.client import get_metric, request
from chaos.harness.procs import Proc, Stack, free_port, python


class _Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path == "/ok":
            body = b'{"hello": "world"}'
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif self.path == "/err":
            self.send_response(503)
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif self.path == "/slow":
            time.sleep(2)
            self.send_response(200)
            self.end_headers()
        elif self.path == "/hangup":
            self.connection.close()


@pytest.fixture
def server():
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{srv.server_address[1]}"
    srv.shutdown()


class TestClientClassifiesWhatItSees:
    def test_an_answer(self, server):
        o = request("GET", server + "/ok")
        assert (o.kind, o.status, o.body) == ("ok", 200, {"hello": "world"}) and o.ok and o.answered

    def test_an_error_status_is_still_an_answer(self, server):
        o = request("GET", server + "/err")
        assert (o.kind, o.status) == ("http_error", 503) and not o.ok and o.answered

    def test_no_answer_in_time_is_a_timeout(self, server):
        o = request("GET", server + "/slow", timeout=0.3)
        assert o.kind == "timeout" and not o.answered and 0.25 < o.elapsed < 1.5

    def test_a_closed_connection_is_a_connection_error(self, server):
        o = request("GET", server + "/hangup")
        assert o.kind == "conn_error" and not o.answered

    def test_nothing_listening_is_a_connection_error(self):
        o = request("GET", f"http://127.0.0.1:{free_port()}/x", timeout=1)
        assert o.kind == "conn_error"


class TestMetricParsing:
    PAGE = (
        "# HELP x_total things\n# TYPE x_total counter\n"
        'x_total{a="1",b="2"} 5\nx_total{a="1"} 7\nx_total 9\nx_total_extra 99\n'
        'h_bucket{le="0.5"} 3\nh_sum 1.5\n'
    )

    def test_exact_labels_and_name(self):
        assert get_metric(self.PAGE, "x_total", a="1", b="2") == 5
        assert get_metric(self.PAGE, "x_total", a="1") == 7
        assert get_metric(self.PAGE, "x_total") == 9

    def test_a_longer_name_is_not_confused_for_a_shorter_one(self):
        assert get_metric(self.PAGE, "x_total_extra") == 99
        assert get_metric(self.PAGE, "x") is None

    def test_absent_is_none(self):
        assert get_metric(self.PAGE, "x_total", a="nope") is None
        assert get_metric(self.PAGE, "h_sum") == 1.5


class TestProcessFaults:
    def sleeper(self, tmp_path):
        return Proc("sleeper", [python(), "-c", "import time; time.sleep(60)"], tmp_path)

    def test_kill9_leaves_no_chance_to_clean_up(self, tmp_path):
        marker = tmp_path / "cleanup-ran"
        code = f"import signal, time\nsignal.signal(signal.SIGTERM, lambda *a: open({str(marker)!r}, 'w').close())\ntime.sleep(60)"
        p = Proc("victim", [python(), "-c", code], tmp_path).start()
        time.sleep(0.3)
        p.kill9()
        assert not p.running() and not marker.exists()

    def test_pause_and_resume(self, tmp_path):
        counter = tmp_path / "ticks"
        code = f"import time\nwhile True:\n    open({str(counter)!r}, 'a').write('x')\n    time.sleep(0.05)"
        p = Proc("ticker", [python(), "-c", code], tmp_path).start()
        time.sleep(0.5)
        p.pause()
        time.sleep(0.2)
        frozen = counter.stat().st_size
        time.sleep(0.6)
        assert counter.stat().st_size == frozen, "a paused process must do nothing"
        p.resume()
        time.sleep(0.5)
        assert counter.stat().st_size > frozen
        p.kill9()

    def test_restart_starts_a_fresh_process(self, tmp_path):
        p = self.sleeper(tmp_path).start()
        first = p.pid
        p.restart()
        assert p.pid != first and p.running() and p.starts == 2
        p.kill9()

    def test_the_stack_stops_everything_even_a_paused_process(self, tmp_path):
        with Stack(tmp_path) as stack:
            p = stack.add(self.sleeper(tmp_path).start())
            stack.add(self.sleeper(tmp_path).start()).pause()
            pid = p.pid
        assert not p.running()
        assert all(not q.running() for q in stack.procs), pid

    def test_wait_http_reports_a_process_that_died_instead_of_waiting_forever(self, tmp_path):
        p = Proc("dies", [python(), "-c", "raise SystemExit(3)"], tmp_path).start()
        with pytest.raises(RuntimeError, match="exited early"):
            p.wait_http(f"http://127.0.0.1:{free_port()}/", timeout=5)
