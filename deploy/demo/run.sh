#!/bin/sh
# Starts the mock model (and the co-pilot's scripted stand-in model), the demo company's tools and agents, then Bifrost with Gjallarhorn.
# Bifrost reads the agent cards and connects to the tools at startup, so they start first.
set -e
: "${GJ_TICKET_KEY:?set GJ_TICKET_KEY (at least 32 characters)}"
: "${GJ_ADMIN_TOKEN:?set GJ_ADMIN_TOKEN (at least 24 characters)}"

/app/mockmodel -addr 127.0.0.1:9000 &
/app/demoworld -gateway http://127.0.0.1:8080 -host 127.0.0.1 -tools 0.0.0.0:9100 &
# The console's demo panel reaches this driver through the control API (/demo/, admin token required).
/app/attacklab -serve 127.0.0.1:9300 &

for port in 9000 9100 9201 9202 9203 9204 9300; do
  i=0
  until curl -s -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 50 ]; then echo "demo service on port ${port} did not start"; exit 1; fi
    sleep 0.2
  done
done
if [ "${GJ_PLUGIN:-on}" = "off" ]; then
  # Overhead measurements only: the same stack with the Gjallarhorn plugin switched off.
  sed -i 's/^      "enabled": true,$/      "enabled": false,/' /app/data/config.json
  grep -q '"enabled": false,' /app/data/config.json || { echo "could not switch the plugin off"; exit 1; }
  echo "Gjallarhorn plugin is OFF (measurement mode)"
fi
echo "demo world ready; starting Bifrost"
exec /app/bifrost-http -app-dir /app/data -port 8080 -host 0.0.0.0 -log-level info -log-style json
