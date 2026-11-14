#!/usr/bin/env bash
# Integration tests: real Bifrost + the Gjallarhorn plugin + a mock model.
#   bash deploy/dev/test.sh        (expects the image gjallarhorn-dev)
set -uo pipefail

ENGINE=${ENGINE:-podman}
NAME=gjallarhorn-dev-test
GW=${GW_PORT:-18081}
CP=${CP_PORT:-18787}
BASE="http://localhost:${GW}"
CTRL="http://localhost:${CP}"
ADMIN="dev-admin-token-$(date +%s)-0123456789"
pass=0; fail=0
ok()  { echo "  PASS  $*"; pass=$((pass+1)); }
bad() { echo "  FAIL  $*"; fail=$((fail+1)); }

$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true
$ENGINE run -d --name "$NAME" -p "${GW}:8080" -p "${CP}:8787" \
  -e GJ_TICKET_KEY="dev-ticket-key-0123456789abcdef0123456789" -e GJ_ADMIN_TOKEN="$ADMIN" \
  gjallarhorn-dev >/dev/null || { echo "could not start container"; exit 1; }
trap '$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true' EXIT

for i in $(seq 1 60); do
  curl -fsS "$BASE/health" >/dev/null 2>&1 && curl -fsS "$CTRL/v1/health" >/dev/null 2>&1 && break
  sleep 1
done

chat() { # chat <virtual-key> <model> -> prints HTTP status, body in /tmp/gj_body.json
  curl -s -o /tmp/gj_body.json -w '%{http_code}' "$BASE/v1/chat/completions" \
    -H 'Content-Type: application/json' -H "x-bf-vk: $1" -H "x-gjallarhorn-label: ${3:-benign}" \
    -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
}
ctl() { curl -s -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' -H 'X-Operator: human:priya' "$@"; }

echo "1. Plugin loaded and control API up"
if $ENGINE logs "$NAME" 2>&1 | grep -q "gjallarhorn .*: ready"; then ok "plugin ready inside Bifrost"; else bad "plugin not ready"; $ENGINE logs "$NAME" 2>&1 | tail -30; fi
if curl -fsS "$CTRL/v1/health" | grep -q '"status": "ok"'; then ok "control API healthy"; else bad "control API not healthy"; fi
code=$(curl -s -o /dev/null -w '%{http_code}' "$CTRL/v1/lockdowns")
[ "$code" = "401" ] && ok "control API rejects calls without a token" || bad "control API without token returned $code"

echo "2. Normal traffic flows"
c=$(chat sk-bf-priya-dev mock/standard-model); [ "$c" = "200" ] && ok "operator's model call allowed" || bad "operator call returned $c: $(cat /tmp/gj_body.json)"
c=$(chat sk-bf-research-dev mock/standard-model); [ "$c" = "200" ] && ok "research agent's standard model call allowed" || bad "research standard call returned $c"

echo "3. Policy rule blocks"
c=$(chat sk-bf-research-dev mock/premium-model attack)
if [ "$c" = "403" ] && grep -q "no-premium-models-for-research\|standard model only" /tmp/gj_body.json; then ok "rule blocked the research agent's premium call (403)"; else bad "premium call returned $c: $(cat /tmp/gj_body.json)"; fi

echo "4. Lockdown: the agent is cut off, people are not"
r=$(ctl -X POST "$CTRL/v1/lockdowns" -d '{"principal":"agent:support","reason":"integration test"}' -o /dev/null -w '%{http_code}')
[ "$r" = "201" ] && ok "lockdown created via control API" || bad "lockdown create returned $r"
c=$(chat sk-bf-support-dev mock/standard-model attack)
if [ "$c" = "403" ] && grep -q "locked down" /tmp/gj_body.json; then ok "locked agent's very next call is blocked"; else bad "locked agent call returned $c: $(cat /tmp/gj_body.json)"; fi
c=$(chat sk-bf-priya-dev mock/standard-model); [ "$c" = "200" ] && ok "operator still works during the lockdown" || bad "operator call during lockdown returned $c"
r=$(ctl -X DELETE "$CTRL/v1/lockdowns/agent:support" -o /dev/null -w '%{http_code}')
[ "$r" = "204" ] && ok "lockdown lifted" || bad "unlock returned $r"
c=$(chat sk-bf-support-dev mock/standard-model); [ "$c" = "200" ] && ok "agent works again after unlock" || bad "after unlock returned $c"

echo "5. Rule-triggered lockdown"
c=$(chat sk-bf-billing-dev mock/shell-runner attack)
[ "$c" = "403" ] && ok "shell access blocked" || bad "shell call returned $c"
if ctl "$CTRL/v1/lockdowns" | grep -q '"principal": "agent:billing"'; then ok "the rule locked the billing agent automatically"; else bad "billing agent was not locked"; fi

echo "6. Flight recorder"
events=$(ctl "$CTRL/v1/events?limit=1000")
n=$(echo "$events" | grep -c '"type": "decision"')
[ "$n" -eq 7 ] && ok "every call recorded exactly once ($n decisions for 7 calls)" || bad "$n decisions recorded for 7 calls"
if echo "$events" | grep -q "sk-bf-"; then bad "a raw virtual key leaked into the events"; else ok "no raw virtual keys in recorded events"; fi
sim=$(ctl -X POST "$CTRL/v1/simulate" -d '{"policy":{"version":1}}')
if echo "$sim" | grep -q '"newly_allowed"'; then ok "simulation over real recorded traffic works"; else bad "simulation failed: $sim"; fi

echo
echo "Result: $pass passed, $fail failed"
[ "$fail" = "0" ]
