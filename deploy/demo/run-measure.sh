#!/usr/bin/env bash
# Measures, in the real stack, how fast a lockdown takes effect and how much latency the
# Gjallarhorn plugin adds to a call. Raw samples are saved to results/.
#
# Overhead is measured in rounds that alternate plugin off and plugin on, each in a fresh
# container, and the samples are pooled, so one unusually slow or fast start can't decide it.
#
#   bash deploy/demo/run-measure.sh            (expects the image gjallarhorn-demo)
#   ROUNDS=5 N=3000 TRIALS=60 bash deploy/demo/run-measure.sh
set -uo pipefail
export MSYS_NO_PATHCONV=1

ENGINE=${ENGINE:-podman}
NAME=gjallarhorn-measure
GW=${GW_PORT:-18082}
CP=${CP_PORT:-18788}
ADMIN=${GJ_ADMIN_TOKEN:-demo-admin-token-0123456789abcdef}
N=${N:-2000}
TRIALS=${TRIALS:-40}
ROUNDS=${ROUNDS:-3}
TS=$(date -u +%Y%m%dT%H%M%SZ)
OUT="results/measure-$TS"
mkdir -p "$OUT"

start() { # $1 = on|off
  $ENGINE rm -f "$NAME" >/dev/null 2>&1 || true
  $ENGINE run -d --name "$NAME" -p "${GW}:8080" -p "${CP}:8787" -e GJ_PLUGIN="$1" \
    -e GJ_TICKET_KEY="demo-ticket-key-0123456789abcdef01234567" -e GJ_ADMIN_TOKEN="$ADMIN" \
    gjallarhorn-demo >/dev/null || { echo "could not start the container"; exit 1; }
  for i in $(seq 1 90); do
    if curl -fsS "http://localhost:${GW}/health" >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if [ "$1" = "on" ]; then
    # The plugin starts after Bifrost's own start-up work (migrations can take a while).
    for i in $(seq 1 60); do
      if curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then break; fi
      sleep 1
    done
    if ! curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then
      echo "Gjallarhorn did not come up"; $ENGINE logs "$NAME" 2>&1 | tail -30; exit 1
    fi
  fi
  sleep 3 # let Bifrost finish connecting to the tools and agents
  if [ "$1" = "off" ] && curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then
    echo "the plugin should be off, but the control API answers"; exit 1
  fi
}
run() { $ENGINE exec -e GJ_ADMIN_TOKEN="$ADMIN" "$NAME" /app/measure "$@"; }
trap '$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true' EXIT

OFFS=""
ONS=""
for r in $(seq 1 "$ROUNDS"); do
  for mode in off on; do
    echo "== round $r, plugin $mode"
    start "$mode"
    run -mode overhead -n "$N" -label "plugin-$mode-round-$r" -out "/tmp/$mode.json" || exit 1
    $ENGINE cp "$NAME:/tmp/$mode.json" "$OUT/overhead-$mode-$r.json"
    if [ "$mode" = off ]; then OFFS="$OFFS,/tmp/in/overhead-off-$r.json"; else ONS="$ONS,/tmp/in/overhead-on-$r.json"; fi
  done
done

echo "== lockdown speed (plugin on)"
run -mode lockdown -n "$TRIALS" -out /tmp/lockdown.json || exit 1
$ENGINE cp "$NAME:/tmp/lockdown.json" "$OUT/lockdown.json"

echo "== comparison, pooled over $ROUNDS rounds"
$ENGINE exec "$NAME" mkdir -p /tmp/in
for f in "$OUT"/overhead-*.json; do $ENGINE cp "$f" "$NAME:/tmp/in/$(basename "$f")"; done
run -mode compare -a "${OFFS#,}" -b "${ONS#,}" -out /tmp/compare.json || exit 1
$ENGINE cp "$NAME:/tmp/compare.json" "$OUT/overhead-compare.json"
echo "saved $OUT/"
