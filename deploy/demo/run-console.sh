#!/usr/bin/env bash
# Starts the full demo in the background and prints where to open the operator console.
#   bash deploy/demo/run-console.sh        (expects the image gjallarhorn-demo)
#   bash deploy/demo/run-console.sh stop
set -uo pipefail
export MSYS_NO_PATHCONV=1

ENGINE=${ENGINE:-podman}
NAME=${NAME:-gjallarhorn-demo}
GW=${GW_PORT:-18082}
CP=${CP_PORT:-18788}
ADMIN=${GJ_ADMIN_TOKEN:-demo-admin-token-0123456789abcdef}

if [ "${1:-}" = "stop" ]; then
  $ENGINE rm -f "$NAME" >/dev/null 2>&1 && echo "stopped"
  exit 0
fi

$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true
$ENGINE run -d --name "$NAME" -p "${GW}:8080" -p "${CP}:8787" \
  -e GJ_TICKET_KEY="demo-ticket-key-0123456789abcdef01234567" -e GJ_ADMIN_TOKEN="$ADMIN" \
  gjallarhorn-demo >/dev/null || { echo "could not start the demo container"; exit 1; }

echo "Waiting for Bifrost and Gjallarhorn..."
for i in $(seq 1 90); do
  if curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -fsS "http://localhost:${CP}/v1/health" >/dev/null 2>&1; then
  echo "the stack did not come up; last log lines:"; $ENGINE logs "$NAME" 2>&1 | tail -40; exit 1
fi

cat <<MSG

Gjallarhorn is running.

  Console:      http://localhost:${CP}/
  Admin token:  ${ADMIN}
  Gateway:      http://localhost:${GW}/  (Bifrost with the Gjallarhorn plugin)

In the console, open "Demo traffic", turn on normal work, then launch an attack.
Stop it with: bash deploy/demo/run-console.sh stop
MSG
