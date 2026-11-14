#!/bin/sh
# Starts the mock model server and Bifrost in one container for the feasibility spike.
set -e
/app/mock -addr 127.0.0.1:9000 &
exec /app/bifrost-http -app-dir /app/data -port 8080 -host 0.0.0.0 -log-level info -log-style json
