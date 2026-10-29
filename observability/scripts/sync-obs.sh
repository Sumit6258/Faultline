#!/usr/bin/env bash
# Copies the canonical metrics kit into every module listed in modules.txt.
#
# Faultline modules never import each other, so shared code is copied rather
# than imported. That keeps each lab and system self contained and runnable
# alone, at the price of duplication, which scripts/check-obs-sync.sh and CI
# keep honest: edit observability/kit/go/obs, run this, commit the result.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
kit="$root/observability/kit/go/obs"

while read -r mod; do
  case "$mod" in ''|\#*) continue ;; esac
  dest="$root/$mod/internal/obs"
  mkdir -p "$dest"
  rm -f "$dest"/*.go
  cp "$kit"/*.go "$dest"/
  echo "synced $mod"
done < "$root/observability/modules.txt"
