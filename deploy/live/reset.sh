#!/usr/bin/env bash
# Puts the public demo back to its starting state: restarts the demo container, which clears
# lockdowns, policy edits, grants and recorded traffic. Run it every hour from cron:
#
#   0 * * * * cd /opt/gjallarhorn && bash deploy/live/reset.sh >> /var/log/gjallarhorn-reset.log 2>&1
set -euo pipefail
cd "$(dirname "$0")/../.."
ENGINE=${ENGINE:-docker}
$ENGINE compose -f deploy/live/compose.yaml --env-file deploy/live/.env up -d --force-recreate --no-deps demo
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) demo reset"
