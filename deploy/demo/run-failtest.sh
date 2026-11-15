#!/usr/bin/env bash
# What happens when Gjallarhorn fails to start? Starts the demo with a signing key that is too
# short (so the plugin's Init fails), first without the guard, then with it, and records what
# Bifrost does in results/startup-failure-<time>.txt.
#   bash deploy/demo/run-failtest.sh        (expects the image gjallarhorn-demo)
set -uo pipefail
export MSYS_NO_PATHCONV=1
ENGINE=${ENGINE:-podman}
NAME=gjallarhorn-failtest
GW=${GW_PORT:-18092}
TS=$(date -u +%Y%m%dT%H%M%SZ)
OUT="results/startup-failure-$TS.txt"
mkdir -p results
trap '$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true' EXIT

call() { # $1 = label, then curl arguments
  printf '%s: HTTP %s\n' "$1" "$(curl -s -o /dev/null -m 10 -w '%{http_code}' "${@:2}")"
}

{
  echo "# Gjallarhorn start-up failure test, $(date -u +%Y-%m-%dT%H:%MZ), commit $(git rev-parse --short HEAD 2>/dev/null)"
  echo "# The plugin's Init fails because GJ_TICKET_KEY is shorter than 32 bytes."
  echo
  echo "## 1. Without the guard (GJ_REQUIRE_PLUGIN=0): what Bifrost does on its own"
  $ENGINE rm -f "$NAME" >/dev/null 2>&1
  $ENGINE run -d --name "$NAME" -p "${GW}:8080" -e GJ_TICKET_KEY=too-short -e GJ_ADMIN_TOKEN=demo-admin-token-0123456789abcdef \
    -e GJ_REQUIRE_PLUGIN=0 gjallarhorn-demo >/dev/null
  for i in $(seq 1 90); do curl -fsS "http://localhost:${GW}/health" >/dev/null 2>&1 && break; sleep 1; done
  sleep 3
  $ENGINE logs "$NAME" 2>&1 | grep -E "failed to load plugin" | cut -c1-300
  call "gateway /health" "http://localhost:${GW}/health"
  call "model call, no Gjallarhorn check" -H 'Content-Type: application/json' -H 'x-bf-vk: sk-bf-research-demo' \
    -d '{"model":"mock/premium-model","messages":[{"role":"user","content":"hi"}]}' "http://localhost:${GW}/v1/chat/completions"
  echo "(research may not use premium models; with Gjallarhorn running this call is refused with 403)"
  echo "container: $($ENGINE inspect "$NAME" --format '{{.State.Status}}')"
  $ENGINE rm -f "$NAME" >/dev/null 2>&1
  echo
  echo "## 2. With the guard (default): the container stops instead"
  $ENGINE run -d --name "$NAME" -p "${GW}:8080" -e GJ_TICKET_KEY=too-short -e GJ_ADMIN_TOKEN=demo-admin-token-0123456789abcdef \
    -e GJ_START_TIMEOUT=30 gjallarhorn-demo >/dev/null
  s=$(date +%s)
  $ENGINE wait "$NAME" >/dev/null 2>&1
  echo "container exited with code $($ENGINE inspect "$NAME" --format '{{.State.ExitCode}}') after $(( $(date +%s) - s ))s"
  $ENGINE logs "$NAME" 2>&1 | grep -E "failed to load plugin|gjallarhorn-guard" | cut -c1-300
} | tee "$OUT"
echo "saved $OUT"
