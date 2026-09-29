#!/usr/bin/env bash
#
# Verify, in a real browser, what a reader sees when the gateway restarts in the middle of a
# run: the run must CONTINUE on its own, and the session must SHOW where it got to.
#
#   ./verify-resume.sh [cdp-port]
#
# It seeds an isolated HOME with a session that was interrupted (running=true and an open turn
# carrying the work done so far), starts the gateway and a slow simulated model, opens the
# session in headless Chrome and measures:
#   1. the gateway resumed the run by itself (a run exists, no client started it);
#   2. the page shows the run as working, and the trail the earlier life left is on screen;
#   3. the open turn is NOT drawn twice (once as history, once as live trail);
#   4. the run's first prompt carries the earlier work (the mock records every prompt);
#   5. when the run ends the record on disk says idle, so the NEXT start does not run it again.
#
# The gateway is one this script starts and owns, with its own HOME: pointing it at the live
# ~/.motita would inject turns into the user's real conversations.
set -uo pipefail

CDP_PORT="${1:-9356}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-resume}"
CHROME="${CHROME:-$HOME/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell}"
PORT_LLM="${PORT_LLM:-8621}"
PORT_GW="${PORT_GW:-8622}"
BASE="http://127.0.0.1:$PORT_GW"
HOME_DIR="$OUT/home"
SID="sresume0000000000000000001"
MOCK_DELAY_MS="${MOCK_DELAY_MS:-7000}"

fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }

LLM_PID=""; GW_PID=""; CH_PID=""; PROXY_PID=""
cleanup() {
  [ -n "$CH_PID" ]  && kill "$CH_PID"  2>/dev/null
  [ -n "$GW_PID" ]  && kill "$GW_PID"  2>/dev/null
  [ -n "$LLM_PID" ] && kill "$LLM_PID" 2>/dev/null
  [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null
  return 0
}
trap cleanup EXIT

export PATH="${PATH}:/home/madkoding/.hermes/cache/go/bin"
command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }
[ -x "$CHROME" ] || { echo "SKIP: no chrome-headless-shell at $CHROME"; exit 2; }
command -v node >/dev/null 2>&1 || { echo "SKIP: node is needed to drive the browser"; exit 2; }
WS_MODULE="${WS_MODULE:-$HOME/.hermes/hermes-agent/node_modules/ws}"
[ -d "$WS_MODULE" ] || { echo "SKIP: no ws module at $WS_MODULE"; exit 2; }

rm -rf "$OUT" && mkdir -p "$HOME_DIR/.motita/sessions" "$OUT/work"
go build -o "$OUT/motita" ./cmd/agent || { echo "ERROR: the agent does not build"; exit 2; }
go build -o "$OUT/mockllm" ./tools/mockllm || { echo "ERROR: the simulated LLM does not build"; exit 2; }

TASK="add the create button to the admin page"
cat > "$HOME_DIR/.motita/sessions/$SID.json" <<JSON
{
  "id": "$SID", "title": "Resume check",
  "created": "2026-09-29T10:00:00Z", "last_used": "2026-09-29T10:30:00Z",
  "running": true, "last_task": "$TASK", "last_kind": "task",
  "turns": [{"User": "$TASK", "Kind": "task", "Pending": true,
    "Agent": "(no result recorded yet: the run is still working, or it was interrupted before it finished)\nWork so far:\nround 1: cat -n lib/api.ts\nround 2: git apply SCHEMA-PATCH-MARKER"}]
}
JSON
printf 'x\n' > "$OUT/task.txt"
cat > "$HOME_DIR/config.yaml" <<YAML
task_source: {kind: file, path: $OUT/task.txt}
anchor: {kind: command, command: sh, args: ["-c", "echo ANCHOR_OK"], expect_exit: 0, expect_output: "ANCHOR_OK", timeout: 30s}
sandbox: {kind: none}
llm: {provider: openai, model: simulated, base_url: "http://127.0.0.1:$PORT_LLM/v1", max_tokens: 512, temperature: 0.0, timeout: 30s, max_attempts: 1}
agent: {max_retries: 1, workspace_dir: $OUT/work, log_level: info, log_console: false}
gateway: {enabled: true, listen: "127.0.0.1:$PORT_GW", token_file: "$HOME_DIR/.motita/gateway.token", allow: [], max_body_kb: 256}
YAML

# The simulated model listens one port up; a tiny proxy on PORT_LLM records every prompt the
# gateway sends, because "the first prompt carries the earlier work" is a claim about bytes.
PORT_MOCK=$((PORT_LLM + 100))
HOME="$HOME_DIR" "$OUT/mockllm" -host 127.0.0.1 -port "$PORT_MOCK" -delay-ms "$MOCK_DELAY_MS" >"$OUT/mock.log" 2>&1 &
LLM_PID=$!
python3 "$REPO/scripts/prompt_proxy.py" "$PORT_LLM" "$PORT_MOCK" "$OUT/prompts.log" >"$OUT/proxy.log" 2>&1 &
PROXY_PID=$!
sleep 0.6
NO_COLOR=1 HOME="$HOME_DIR" MOTITA_LLM_API_KEY=test "$OUT/motita" -config "$HOME_DIR/config.yaml" -serve >"$OUT/gateway.log" 2>&1 &
GW_PID=$!

TOKEN_FILE="$HOME_DIR/.motita/gateway.token"
deadline=$((SECONDS + 45))
while [ "$SECONDS" -lt "$deadline" ]; do
  [ -s "$TOKEN_FILE" ] && curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null && break
  sleep 0.2
done
TOKEN="$(tr -d '\n' < "$TOKEN_FILE")"
[ -n "$TOKEN" ] || { echo "ERROR: the gateway never wrote its token"; exit 2; }
api() { curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE$1"; }

echo "==> 1. the gateway resumed the run by itself"
grep -rq "resuming interrupted session" "$HOME_DIR/.motita/workspace/motita.log" "$OUT/gateway.log" 2>/dev/null && ok "the log says it resumed $SID" || bad "no resume was logged"
if api "/v1/sessions/$SID/run" >"$OUT/run.json" 2>/dev/null; then ok "a run is in flight with no client having started it"; else bad "no run in flight after the restart"; fi

echo "==> 2-3. what the page shows"
"$CHROME" --headless --no-sandbox --disable-gpu --remote-debugging-port="$CDP_PORT" \
  --user-data-dir="$OUT/chrome" --window-size=1400,900 about:blank >"$OUT/chrome.log" 2>&1 &
CH_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$CDP_PORT/json/list" >/dev/null 2>&1 && break; sleep 0.2; done

WS_MODULE="$WS_MODULE" CDP_PORT="$CDP_PORT" BASE="$BASE" TOKEN="$TOKEN" SID="$SID" OUT="$OUT" \
  node "$REPO/scripts/cdp_resume.js" >"$OUT/browser.txt" 2>&1
sed 's/^/  /' "$OUT/browser.txt"
grep -q '^PASS running-indicator' "$OUT/browser.txt" && ok "the page shows the session as working" || bad "the page does not show the run"
grep -q '^PASS earlier-work-visible' "$OUT/browser.txt" && ok "the earlier work is on screen" || bad "the earlier work is not on screen"
grep -q '^PASS not-drawn-twice' "$OUT/browser.txt" && ok "the open turn is drawn once" || bad "the open turn is drawn twice"

echo "==> 5. when it ends, the record says idle (waiting for the run: one slow reply per phase)"
deadline=$((SECONDS + 90))
while [ "$SECONDS" -lt "$deadline" ]; do
  api "/v1/sessions/$SID/run" >/dev/null 2>&1 || break
  sleep 0.5
done
sleep 1
echo "==> 4. the model was told what had been done"
if grep -q "SCHEMA-PATCH-MARKER" "$OUT/prompts.log" 2>/dev/null; then ok "the first prompt carries the earlier work"; else bad "the resumed run started from nothing"; fi

if python3 - "$HOME_DIR/.motita/sessions/$SID.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
turns = d.get("turns") or []
sys.exit(0 if not d.get("running") and turns and not turns[-1].get("Pending") else 1)
PY
then ok "running=false and the turn is closed: the next start will not run it again"; else bad "the record on disk still says running, or the turn is still open"; fi

echo
if [ "$fail" -eq 0 ]; then echo "verify-resume: all checks passed"; else echo "verify-resume: $fail check(s) FAILED (artifacts in $OUT)"; fi
exit "$fail"
