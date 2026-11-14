#!/usr/bin/env bash
# Starts the demo container and runs the attack lab inside it (monitor-only baseline, then
# enforcing). The JSON report is copied to results/.
#   bash deploy/demo/run-lab.sh            (expects the image gjallarhorn-demo)
#   KEEP=1 bash deploy/demo/run-lab.sh     (leave the container running afterwards)
set -uo pipefail
# Git Bash on Windows rewrites paths like /app/x into Windows paths; container paths must stay as they are.
export MSYS_NO_PATHCONV=1

ENGINE=${ENGINE:-podman}
NAME=${NAME:-gjallarhorn-demo}
GW=${GW_PORT:-18082}
CP=${CP_PORT:-18788}
ADMIN=${GJ_ADMIN_TOKEN:-demo-admin-token-0123456789abcdef}
REPORT="results/attacklab-$(date -u +%Y%m%dT%H%M%SZ).json"

$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true
$ENGINE run -d --name "$NAME" -p "${GW}:8080" -p "${CP}:8787" \
  -e GJ_TICKET_KEY="demo-ticket-key-0123456789abcdef01234567" -e GJ_ADMIN_TOKEN="$ADMIN" \
  gjallarhorn-demo >/dev/null || { echo "could not start the demo container"; exit 1; }
if [ -z "${KEEP:-}" ]; then trap '$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true' EXIT; fi

echo "Waiting for Bifrost and Gjallarhorn..."
for i in $(seq 1 90); do
  if curl -fsS "http://localhost:${GW}/health" >/dev/null 2>&1 && curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then
  echo "the stack did not come up; last log lines:"; $ENGINE logs "$NAME" 2>&1 | tail -40; exit 1
fi

$ENGINE exec -e GJ_ADMIN_TOKEN="$ADMIN" "$NAME" /app/attacklab -mode "${MODE:-both}" ${ONLY:+-only "$ONLY"} -out /tmp/report.json
status=$?
mkdir -p results
$ENGINE cp "$NAME:/tmp/report.json" "$REPORT" >/dev/null 2>&1 && echo "report: $REPORT"
exit $status
