#!/usr/bin/env bash
# Phase 1 feasibility checks against the spike container.
#   bash deploy/spike/test.sh            (builds nothing; expects the image gjallarhorn-spike)
set -uo pipefail

ENGINE=${ENGINE:-podman}
NAME=gjallarhorn-spike-run
PORT=${PORT:-18080}
BASE="http://localhost:${PORT}"
pass=0; fail=0
ok()   { echo "  PASS  $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $*"; fail=$((fail+1)); }

$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true
$ENGINE run -d --name "$NAME" -p "${PORT}:8080" gjallarhorn-spike >/dev/null || { echo "could not start container"; exit 1; }
trap '$ENGINE rm -f "$NAME" >/dev/null 2>&1 || true' EXIT

echo "Waiting for Bifrost..."
for i in $(seq 1 60); do
  if curl -fsS "$BASE/health" >/dev/null 2>&1; then break; fi
  sleep 1
done

logs() { $ENGINE logs "$NAME" 2>&1; }

echo "1. Plugin loads"
if logs | grep -q 'GJSPIKE {.*"kind":"init"'; then ok "plugin Init ran inside Bifrost"; else bad "plugin did not initialise"; logs | tail -40; fi

echo "2. Allowed call reaches the model"
code=$(curl -s -o /tmp/gj_allowed.json -w '%{http_code}' "$BASE/v1/chat/completions" \
  -H 'Content-Type: application/json' -H 'x-bf-vk: sk-bf-spike-test-key' -H 'x-gjallarhorn-chain: from-agent' \
  -d '{"model":"mock/allowed-model","messages":[{"role":"user","content":"hi"}]}')
if [ "$code" = "200" ] && grep -q "hello from the mock model" /tmp/gj_allowed.json; then ok "allowed model answered (HTTP 200)"; else bad "allowed call returned HTTP $code: $(cat /tmp/gj_allowed.json)"; fi

echo "3. Plugin sees the virtual key and headers"
line=$(logs | grep 'GJSPIKE {.*"kind":"pre_llm".*allowed-model' | tail -1)
want_fp=$(printf '%s' 'sk-bf-spike-test-key' | sha256sum | cut -c1-12)
if echo "$line" | grep -q "\"vk_fp\":\"$want_fp\""; then ok "virtual key visible in PreLLMHook"; else bad "virtual key not visible: $line"; fi
if echo "$line" | grep -q '"chain_header":"from-agent"'; then ok "custom request header visible in PreLLMHook"; else bad "custom header not visible: $line"; fi

echo "4. Blocked call is short-circuited without a fallback"
before=$(logs | grep -c 'MOCK call=')
code=$(curl -s -o /tmp/gj_blocked.json -w '%{http_code}' "$BASE/v1/chat/completions" \
  -H 'Content-Type: application/json' -H 'x-bf-vk: sk-bf-spike-test-key' \
  -d '{"model":"mock/blocked-model","messages":[{"role":"user","content":"hi"}],"fallbacks":["mock/allowed-model"]}')
after=$(logs | grep -c 'MOCK call=')
if [ "$code" = "403" ] && grep -q "blocked by gjallarhorn-spike" /tmp/gj_blocked.json; then ok "blocked with HTTP 403 and our message"; else bad "blocked call returned HTTP $code: $(cat /tmp/gj_blocked.json)"; fi
if [ "$before" = "$after" ]; then ok "the model was never called and no fallback ran"; else bad "the model was called $((after-before)) time(s) despite the block"; fi

echo
echo "Result: $pass passed, $fail failed"
[ "$fail" = "0" ]
