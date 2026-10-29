#!/usr/bin/env bash
# Starts every Faultline service that publishes metrics, each on its own port,
# so Prometheus can scrape them side by side. They all default to port 8080,
# so each is given an address here. Stop everything with Ctrl-C.
#
# The social feed runs fan out on write, so the celebrity problem shows up on
# the dashboard. Use -strategy hybrid by hand to see it go away.
#
# The payment platform needs its Python dependencies installed first:
#   pip install -r systems/payment-platform/requirements.txt
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
bin="$(mktemp -d)"
pids=()

cleanup() {
  [ "${#pids[@]}" -eq 0 ] || kill "${pids[@]}" 2>/dev/null || true
  rm -rf "$bin"
}
trap cleanup EXIT INT TERM

start_go() { # name, module dir, package, flags...
  local name=$1 dir=$2 pkg=$3
  shift 3
  (cd "$root/$dir" && go build -o "$bin/$name" "$pkg")
  "$bin/$name" "$@" > "$bin/$name.log" 2>&1 &
  pids+=($!)
  echo "started $name"
}

start_go rate-limiting         labs/rate-limiting            ./cmd/server    -addr 127.0.0.1:9101 -algo token-bucket -limit 10
start_go url-shortener         systems/url-shortener         ./cmd/server    -addr 127.0.0.1:9102
start_go notification-platform systems/notification-platform ./cmd/server    -addr 127.0.0.1:9103
start_go social-feed           systems/social-feed           ./cmd/server    -addr 127.0.0.1:9104 -strategy write
start_go lab-server            web/interactive-lab/server    ./cmd/labserver -addr 127.0.0.1:8081

(cd "$root/systems/payment-platform" && PAYMENT_DB_PATH="$bin/payments.db" exec "${PYTHON:-python3}" -m uvicorn app.main:app --host 127.0.0.1 --port 9105 --log-level warning) > "$bin/payment-platform.log" 2>&1 &
pids+=($!)
echo "started payment-platform"

echo
echo "All six are up. Metrics are at http://127.0.0.1:<port>/metrics, ports 9101 to 9105 and 8081."
echo "Logs are in $bin. Press Ctrl-C to stop."
wait
