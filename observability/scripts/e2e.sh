#!/usr/bin/env bash
# Runs the whole observability stack for real, without Docker: every service,
# a Prometheus scraping them, traffic, and then checks that the dashboards'
# queries find data and the alerts fire for real conditions.
#
#   observability/scripts/e2e.sh
#
# It needs go, python3 with the payment platform's requirements installed, and
# the prometheus and promtool binaries (from a Prometheus release). Point at
# them with PROMETHEUS, PROMTOOL and PYTHON if they are not on your PATH.
#
# It runs Prometheus with a one second scrape interval and alert "for"
# durations of five seconds, so a real run takes about a minute instead of
# the quarter hour the production values would need. The rules themselves are
# otherwise exactly the ones in prometheus/rules.
set -uo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
PROMETHEUS="${PROMETHEUS:-prometheus}"
PROMTOOL="${PROMTOOL:-promtool}"
PYTHON="${PYTHON:-python3}"
work="$(mktemp -d)"
pids=()

cleanup() {
  [ "${#pids[@]}" -eq 0 ] || kill "${pids[@]}" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

start_go() { # name, module dir, package, flags...
  local name=$1 dir=$2 pkg=$3
  shift 3
  (cd "$root/$dir" && go build -o "$work/$name" "$pkg") || { echo "build failed: $name"; exit 1; }
  "$work/$name" "$@" > "$work/$name.log" 2>&1 &
  pids+=($!)
  eval "pid_$(echo "$name" | tr - _)=$!"
}

echo "== starting services"
start_go rate-limiting         labs/rate-limiting            ./cmd/server    -addr 127.0.0.1:9101 -algo token-bucket -limit 10
start_go url-shortener         systems/url-shortener         ./cmd/server    -addr 127.0.0.1:9102
start_go notification-platform systems/notification-platform ./cmd/server    -addr 127.0.0.1:9103
start_go social-feed           systems/social-feed           ./cmd/server    -addr 127.0.0.1:9104 -strategy write
start_go lab-server            web/interactive-lab/server    ./cmd/labserver -addr 127.0.0.1:8081
(cd "$root/systems/payment-platform" && PAYMENT_DB_PATH="$work/payments.db" exec "$PYTHON" -m uvicorn app.main:app --host 127.0.0.1 --port 9105 --log-level warning) > "$work/payment-platform.log" 2>&1 &
pids+=($!)

echo "== preparing Prometheus (1s scrapes, 5s alert delays)"
mkdir -p "$work/prom/rules"
sed -e 's/host.docker.internal/127.0.0.1/' -e 's/scrape_interval: 5s/scrape_interval: 1s/' -e 's/evaluation_interval: 5s/evaluation_interval: 1s/' \
  "$root/observability/prometheus/prometheus.yml" > "$work/prom/prometheus.yml"
for f in "$root"/observability/prometheus/rules/*.yml; do
  sed -E 's/^( *for: )[0-9]+m$/\15s/' "$f" > "$work/prom/rules/$(basename "$f")"
done
"$PROMTOOL" check config "$work/prom/prometheus.yml" > /dev/null || { echo "the e2e config does not validate"; exit 1; }
"$PROMETHEUS" --config.file="$work/prom/prometheus.yml" --storage.tsdb.path="$work/prom/data" --web.listen-address=127.0.0.1:9090 --log.level=warn > "$work/prometheus.log" 2>&1 &
pids+=($!)

for i in $(seq 1 30); do curl -sf http://127.0.0.1:9090/-/ready > /dev/null && break; sleep 1; done
for port in 9101 9102 9103 9104 9105 8081; do
  for i in $(seq 1 30); do curl -sf "http://127.0.0.1:$port/metrics" > /dev/null && break; sleep 1; done
done

echo "== traffic for 80 seconds"
"$PYTHON" "$root/observability/scripts/traffic.py" --seconds 80 --celebrity-followers 10000 > "$work/traffic.out" 2>&1 &
traffic=$!
pids+=($traffic)

sleep 40
echo "== lint every service's /metrics with promtool"
"$PYTHON" "$root/observability/scripts/e2e_check.py" lint --promtool "$PROMTOOL" || failed=1

echo "== dashboards, with every service up"
"$PYTHON" "$root/observability/scripts/e2e_check.py" dashboards || failed=1

echo "== stopping rate-limiting on purpose, to see InstanceDown"
kill "$pid_rate_limiting" 2>/dev/null
sleep 12

echo "== alerts"
"$PYTHON" "$root/observability/scripts/e2e_check.py" alerts --expect-down rate-limiting || failed=1
status="${failed:-0}"

wait "$traffic" 2>/dev/null
echo "== traffic summary"
cat "$work/traffic.out"
exit "$status"
