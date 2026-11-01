#!/usr/bin/env bash
#
# Verify, in two real browsers, that a conversation open on two machines stays in step: a turn
# asked from one browser shows up in the other while it runs (the question too), and its answer
# reaches the other when it ends, with no reload.
#
#   ./verify-two-browsers.sh
#
# It starts a gateway of its own (with its own HOME, never the live ~/.motita) and a slow
# simulated model, opens the same session in two separate browser profiles with Playwright, types
# a question in the first one and watches the second.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-two-browsers}"
PORT_LLM="${PORT_LLM:-8631}"
PORT_GW="${PORT_GW:-8632}"
BASE="http://127.0.0.1:$PORT_GW"
HOME_DIR="$OUT/home"
MOCK_DELAY_MS="${MOCK_DELAY_MS:-1500}"

LLM_PID=""; GW_PID=""
cleanup() {
  [ -n "$GW_PID" ]  && kill "$GW_PID"  2>/dev/null
  [ -n "$LLM_PID" ] && kill "$LLM_PID" 2>/dev/null
  return 0
}
trap cleanup EXIT

command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }
node -e "require('playwright')" 2>/dev/null || { echo "SKIP: node cannot load playwright"; exit 2; }

rm -rf "$OUT" && mkdir -p "$HOME_DIR/.motita" "$OUT/work"
go build -o "$OUT/motita" ./cmd/agent || { echo "ERROR: the agent does not build"; exit 2; }
go build -o "$OUT/mockllm" ./tools/mockllm || { echo "ERROR: the simulated LLM does not build"; exit 2; }

printf 'x\n' > "$OUT/task.txt"
cat > "$HOME_DIR/config.yaml" <<YAML
task_source: {kind: file, path: $OUT/task.txt}
anchor: {kind: command, command: sh, args: ["-c", "echo ANCHOR_OK"], expect_exit: 0, expect_output: "ANCHOR_OK", timeout: 30s}
sandbox: {kind: none}
llm: {provider: openai, model: simulated, base_url: "http://127.0.0.1:$PORT_LLM/v1", max_tokens: 512, temperature: 0.0, timeout: 30s, max_attempts: 1}
agent: {max_retries: 1, workspace_dir: $OUT/work, log_level: info, log_console: false}
gateway: {enabled: true, listen: "127.0.0.1:$PORT_GW", token_file: "$HOME_DIR/.motita/gateway.token", allow: [], max_body_kb: 256}
YAML

HOME="$HOME_DIR" "$OUT/mockllm" -host 127.0.0.1 -port "$PORT_LLM" -delay-ms "$MOCK_DELAY_MS" >"$OUT/mock.log" 2>&1 &
LLM_PID=$!
sleep 0.4
NO_COLOR=1 HOME="$HOME_DIR" MOTITA_LLM_API_KEY=test "$OUT/motita" -config "$HOME_DIR/config.yaml" -serve >"$OUT/gateway.log" 2>&1 &
GW_PID=$!

TOKEN_FILE="$HOME_DIR/.motita/gateway.token"
deadline=$((SECONDS + 45))
while [ "$SECONDS" -lt "$deadline" ]; do
  [ -s "$TOKEN_FILE" ] && curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null && break
  sleep 0.2
done
TOKEN="$(tr -d '\n' < "$TOKEN_FILE")"
[ -n "$TOKEN" ] || { echo "ERROR: the gateway never wrote its token"; tail -20 "$OUT/gateway.log"; exit 2; }

BASE="$BASE" TOKEN="$TOKEN" OUT="$OUT" node "$REPO/scripts/two_browsers.js"
