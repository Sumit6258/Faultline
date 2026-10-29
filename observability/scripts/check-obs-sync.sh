#!/usr/bin/env bash
# Fails if any module's copy of the metrics kit differs from the canonical one, or
# if a service carries the kit but its main.go never calls observe(), which is the
# one line that actually serves /metrics. Copying the kit does not wire it in.
set -uo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
kit="$root/observability/kit/go/obs"
status=0

while read -r mod; do
  case "$mod" in ''|\#*) continue ;; esac
  dest="$root/$mod/internal/obs"
  if [ ! -d "$dest" ]; then
    echo "MISSING  $mod has no internal/obs (run observability/scripts/sync-obs.sh)"
    status=1
  elif ! diff -r "$kit" "$dest" > /dev/null; then
    echo "DRIFTED  $mod/internal/obs differs from observability/kit/go/obs:"
    diff -r "$kit" "$dest" | head -20 | sed 's/^/           /'
    status=1
  else
    echo "ok       $mod"
  fi
  for main in "$root/$mod"/cmd/*/main.go; do
    [ -f "$main" ] || continue
    if ! grep -q 'observe(' "$main"; then
      echo "UNWIRED  ${main#"$root"/} never calls observe(...), so this service serves no /metrics"
      status=1
    fi
  done
done < "$root/observability/modules.txt"

[ "$status" -eq 0 ] || echo "Run observability/scripts/sync-obs.sh for drift. For UNWIRED, wrap the handler in main.go with observe(...), see observability/README.md."
exit "$status"
