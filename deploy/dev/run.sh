#!/bin/sh
# Starts the mock model server and Bifrost (with Gjallarhorn) in one container.
set -e
: "${GJ_TICKET_KEY:?set GJ_TICKET_KEY (at least 32 characters)}"
: "${GJ_ADMIN_TOKEN:?set GJ_ADMIN_TOKEN (at least 24 characters)}"
/app/mock -addr 127.0.0.1:9000 &
exec /app/bifrost-http -app-dir /app/data -port 8080 -host 0.0.0.0 -log-level info -log-style json
