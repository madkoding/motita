#!/usr/bin/env bash
#
# End-to-end check of the suggestions flow, against a gateway this script owns (isolated HOME,
# simulated LLM that proposes two follow-ups with MOCK_SUGGESTIONS=1), driven over HTTP with curl:
#
#   1. a task finishes with a report that carries suggestions;
#   2. the answer to them (the buttons pressed) is kept with the turn;
#   3. the turn that applies the accepted ones runs;
#   4. after a gateway RESTART the answer and that turn are still there, so the web page can draw
#      the same marks and the "Applying suggestions" label after a reload.
#
# No browser and no container: what is checked is what the gateway keeps, which is what a page
# reloaded from it draws (the drawing itself is covered by web/src/__tests__/suggestions.test.mjs).
#
#   ./scripts/e2e-suggestions.sh
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-suggestions}"
PORT_LLM="${PORT_LLM:-8641}"
PORT_GW="${PORT_GW:-8642}"
BASE="http://127.0.0.1:$PORT_GW"
HOME_DIR="$OUT/home"
TOKEN_FILE="$HOME_DIR/.motita/gateway.token"

fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }

LLM_PID=""
GW_PID=""
cleanup() {
  [ -n "$GW_PID" ]  && kill "$GW_PID"  2>/dev/null
  [ -n "$LLM_PID" ] && kill "$LLM_PID" 2>/dev/null
  return 0
}
trap cleanup EXIT

command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl is not on the PATH"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "ERROR: python3 is not on the PATH"; exit 2; }

echo "==> Building the agent and the simulated LLM"
cd "$REPO" || exit 2
go build -o "$OUT/motita" ./cmd/agent || { echo "ERROR: the agent does not build"; exit 2; }
go build -o "$OUT/mockllm" ./tools/mockllm || { echo "ERROR: the simulated LLM does not build"; exit 2; }

# A HOME of its own: the gateway keeps its sessions under $HOME/.motita, and this check must not
# write turns into the user's real conversations.
rm -rf "$HOME_DIR" "$OUT/work" && mkdir -p "$HOME_DIR/.motita/sessions" "$OUT/work"
printf 'Leave the report with the requested content.\n' > "$OUT/task.txt"
git -C "$OUT/work" init -q -b main
git -C "$OUT/work" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
cat > "$HOME_DIR/config.yaml" <<YAML
task_source:
  kind: file
  path: $OUT/task.txt
anchor:
  kind: command
  command: sh
  args: ["-c", "echo ANCHOR_OK"]
  expect_exit: 0
  expect_output: "ANCHOR_OK"
  timeout: 30s
sandbox:
  kind: none
llm:
  provider: openai
  model: simulated
  base_url: http://127.0.0.1:$PORT_LLM/v1
  max_tokens: 512
  temperature: 0.0
  timeout: 30s
  max_attempts: 1
agent:
  max_retries: 1
  workspace_dir: $OUT/work
  log_level: info
  log_console: false
gateway:
  enabled: true
  listen: "127.0.0.1:$PORT_GW"
  token_file: "$TOKEN_FILE"
  allow: []
  max_body_kb: 256
YAML

MOCK_SUGGESTIONS=1 HOME="$HOME_DIR" "$OUT/mockllm" -port "$PORT_LLM" > "$OUT/mock.log" 2>&1 &
LLM_PID=$!

# start_gateway starts the gateway and waits for the token file AND a 200 from /v1/health: the
# token is written before the bind, so either alone is not enough.
start_gateway() {
  NO_COLOR=1 HOME="$HOME_DIR" MOTITA_LLM_API_KEY=test \
    "$OUT/motita" -config "$HOME_DIR/config.yaml" -serve >> "$OUT/gateway.log" 2>&1 &
  GW_PID=$!
  local deadline=$((SECONDS + 45))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if [ -s "$TOKEN_FILE" ] && curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
  done
  echo "ERROR: the gateway never came up; its log holds:"
  sed -n 1,40p "$OUT/gateway.log" | sed 's/^/    /'
  exit 2
}

# json reads one value out of the JSON on stdin with a Python expression over `d`.
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

start_gateway
AUTH="Authorization: Bearer $(cat "$TOKEN_FILE")"
ok "the gateway is answering on $BASE"

SID="$(curl -s -X POST -H "$AUTH" "$BASE/v1/sessions" | json 'd["id"]')"
[ -n "$SID" ] && ok "opened a session" || { bad "no session could be opened"; exit 1; }
S="$BASE/v1/sessions/$SID"

echo "== 1. a finished task proposes suggestions =="
curl -s -N -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' "$S/task" > "$OUT/first.sse"
n="$(curl -s -H "$AUTH" "$S/checkpoints" | json 'len(d["checkpoints"][0]["outcome"]["report"].get("suggestions") or [])')"
[ "$n" = "2" ] && ok "the report of turn 0 carries 2 suggestions" || bad "turn 0 has $n suggestions, want 2"

echo "== 2. the answer is kept with the turn =="
REQUEST=$'Apply these suggestions you made, and nothing else:\n- test the empty report: nothing checks it'
ANSWER="$(REQUEST="$REQUEST" python3 -c 'import json,os; print(json.dumps({"picks":["this","none"],"verdicts":["apply","skip"],"request":os.environ["REQUEST"]}))')"
code="$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  -d "$ANSWER" "$S/checkpoints/0/answers")"
[ "$code" = "200" ] && ok "the answer was kept" || bad "keeping the answer answered $code"
code="$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  -d "$ANSWER" "$S/checkpoints/0/answers")"
[ "$code" = "409" ] && ok "a second answer is refused" || bad "a second answer answered $code, want 409"

echo "== 3. the turn that applies the accepted ones runs =="
BODY="$(REQUEST="$REQUEST" python3 -c 'import json,os; print(json.dumps({"task":os.environ["REQUEST"]}))')"
curl -s -N -X POST -H "$AUTH" -H 'Content-Type: application/json' -d "$BODY" "$S/task" > "$OUT/second.sse"
grep -q '"result"' "$OUT/second.sse" && ok "the applying turn finished" || bad "the applying turn did not finish"

echo "== 4. after a restart, the answer and the turn are still there =="
kill "$GW_PID" 2>/dev/null; wait "$GW_PID" 2>/dev/null; GW_PID=""
start_gateway
AUTH="Authorization: Bearer $(cat "$TOKEN_FILE")"
curl -s -H "$AUTH" "$S/checkpoints" > "$OUT/checkpoints.json"
picks="$(json 'd["checkpoints"][0].get("answers",{}).get("picks")' < "$OUT/checkpoints.json")"
[ "$picks" = "['this', 'none']" ] && ok "the buttons pressed survive the restart" || bad "picks after the restart: $picks"
same="$(REQUEST="$REQUEST" python3 -c '
import json, os, sys
d = json.load(open(sys.argv[1]))
cps = {c["turn"]: c for c in d["checkpoints"]}
print(cps[0].get("answers", {}).get("request") == os.environ["REQUEST"] and cps.get(1, {}).get("task") == os.environ["REQUEST"])
' "$OUT/checkpoints.json")"
[ "$same" = "True" ] && ok "the next turn is the request kept with the answer (drawn as \"Applying suggestions\")" \
  || bad "the applying turn does not match the request kept with the answer"

echo
if [ "$fail" = "0" ]; then
  echo "VERDICT: suggestions are proposed, answered once, applied, and survive a restart"
else
  echo "VERDICT: FAILED ($fail) - logs are in $OUT"
fi
exit "$fail"
