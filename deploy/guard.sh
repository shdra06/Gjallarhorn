#!/bin/sh
# Runs Bifrost and makes sure Gjallarhorn is really running inside it.
#
# Bifrost keeps serving traffic when a plugin fails to load (it logs "failed to load plugin"
# and carries on). For a security plugin that means failing open: every call goes through
# unchecked. This guard fails closed instead: if Gjallarhorn's control API does not answer
# within GJ_START_TIMEOUT seconds, or stops answering for GJ_FAILURES checks in a row, Bifrost
# is stopped and the container exits with an error, so your orchestrator restarts it or
# alerts you.
#
#   guard.sh /app/bifrost-http -app-dir /app/data ...
#
# Settings (environment):
#   GJ_REQUIRE_PLUGIN=0   turn the guard off (for example with control.disabled)
#   GJ_HEALTH_URL         default http://127.0.0.1:8787/v1/health
#   GJ_START_TIMEOUT      seconds to wait at start-up, default 120
#   GJ_CHECK_INTERVAL     seconds between checks, default 5
#   GJ_FAILURES           failed checks in a row before stopping, default 3
set -u

if [ "${GJ_REQUIRE_PLUGIN:-1}" = "0" ]; then
  exec "$@"
fi

url="${GJ_HEALTH_URL:-http://127.0.0.1:8787/v1/health}"
start_timeout="${GJ_START_TIMEOUT:-120}"
interval="${GJ_CHECK_INTERVAL:-5}"
max_failures="${GJ_FAILURES:-3}"

"$@" &
pid=$!
trap 'kill -TERM "$pid" 2>/dev/null; wait "$pid"; exit $?' TERM INT

healthy() { curl -fsS -m 3 -o /dev/null "$url" 2>/dev/null; }

stop() {
  echo "gjallarhorn-guard: $1; stopping Bifrost so traffic is not served unchecked" >&2
  kill -TERM "$pid" 2>/dev/null
  i=0
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 20 ]; do sleep 0.5; i=$((i + 1)); done
  kill -KILL "$pid" 2>/dev/null
  exit 1
}

waited=0
until healthy; do
  if ! kill -0 "$pid" 2>/dev/null; then
    wait "$pid"
    exit $?
  fi
  if [ "$waited" -ge "$start_timeout" ]; then
    stop "Gjallarhorn did not start within ${start_timeout}s (check the log above for 'failed to load plugin')"
  fi
  sleep 1
  waited=$((waited + 1))
done
echo "gjallarhorn-guard: Gjallarhorn is running; watching it every ${interval}s" >&2

failures=0
while kill -0 "$pid" 2>/dev/null; do
  sleep "$interval"
  if healthy; then
    failures=0
  else
    failures=$((failures + 1))
    if [ "$failures" -ge "$max_failures" ]; then
      stop "Gjallarhorn stopped answering ($failures failed checks)"
    fi
  fi
done
wait "$pid"
exit $?
