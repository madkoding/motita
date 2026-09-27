#!/usr/bin/env bash
#
# Verify the Markdown rendering against a RUNNING gateway this check starts and owns,
# in a real browser.
#
#   ./verify-markdown.sh [cdp-port]
#
# Why a browser, and why its own gateway: every defect this exists to catch is a
# property of the RENDERED page. A diagram whose source attribute was emptied by the
# sanitizer, a diagram whose labels never made it into the SVG, and a formula whose raw
# LaTeX was printed under it all produce markup that reads as correct, and shapes
# (boxes, edges, arrow markers, <math> elements) that are all there. Only a rendered
# value — a bounding box, a computed colour, a text node — tells the difference.
#
# It seeds its own conversation under a HOME of its own, so it never touches the
# conversations of whoever is running the gate.
set -uo pipefail

CDP_PORT="${1:-9356}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="${SCRATCH:-/home/madkoding/.hermes/cache/scratch}"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-markdown}"
PY="${PY:-$SCRATCH/.cdp/bin/python}"
CHROME="${CHROME:-$HOME/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell}"
PORT_LLM="${PORT_LLM:-8621}"
PORT_GW="${PORT_GW:-8622}"
BASE="http://127.0.0.1:$PORT_GW"
HOME_DIR="$OUT/home"
mkdir -p "$OUT"

fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }

LLM_PID=""
GW_PID=""
CHROME_STARTED=""
cleanup() {
  [ -n "$GW_PID" ]  && kill "$GW_PID"  2>/dev/null
  [ -n "$LLM_PID" ] && kill "$LLM_PID" 2>/dev/null
  # A bare `wait` here waits for EVERY child, and the browser is one and never exits,
  # so the script would hang after printing its verdict.
  [ -n "$CHROME_STARTED" ] && pkill -f "remote-debugging-port=$CDP_PORT" 2>/dev/null
  return 0
}
trap cleanup EXIT

export PATH="${PATH}:/home/madkoding/.hermes/cache/go/bin"
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl is not on the PATH"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }

# A browser this machine does not have is a MISSING TOOL, not a defect in the renderer.
# Exit 2 is the convention verify.sh reads as "skipped", so a contributor without the
# browser gets an honest skip instead of a red gate about a feature that is fine.
if [ ! -x "$CHROME" ]; then
  echo "SKIP: no chrome-headless-shell at $CHROME"
  echo "      set CHROME=... to the one on this machine"
  exit 2
fi

echo "==> Building the agent and the simulated LLM"
go build -o "$OUT/motita" ./cmd/agent || { echo "ERROR: the agent does not build"; exit 2; }
go build -o "$OUT/mockllm" ./tools/mockllm || { echo "ERROR: the simulated LLM does not build"; exit 2; }

# The gateway resolves its state directory as $HOME/.motita — config.Dir() reads HOME
# and hardcodes the ".motita" leaf. There is no MOTITA_HOME, so an isolated HOME is the
# only way to keep this off the real conversations. The seeded session is written
# straight into that HOME's session store, which is what the page then renders.
#
# The browser profile is wiped with it, and that is not housekeeping: the page registers
# a service worker, and a SW persists in the profile across runs. A profile from an
# earlier run holds a precache of THAT build's chunks, so the browser answers the new
# page out of a stale cache and a chunk the build added since — elk, when mermaid went
# from one diagram family to the whole library — is never fetched correctly. That
# presents as "Failed to fetch dynamically imported module" with the file sitting right
# there on disk, served with a 200 by the gateway. A gate that keeps a stale browser
# between runs is measuring the last build, not this one.
echo "==> Seeding a conversation in $HOME_DIR/.motita/sessions"
rm -rf "$HOME_DIR" "$OUT/cdp-profile" "$OUT/work"
mkdir -p "$HOME_DIR/.motita/sessions" "$OUT/work"
printf 'leave the report with the requested content\n' > "$OUT/task.txt"
# sandbox.kind=none: the check is about rendering, and a container would need
# privileges that say nothing about whether a formula is typeset.
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
  token_file: "$HOME_DIR/.motita/gateway.token"
  allow: []
  max_body_kb: 256
YAML

# The session file is written from the SAME fixture the probe asserts against, by
# importing it rather than copying it: a second copy of the document would drift, and
# the gate would then be checking a page the fixture no longer describes.
#
# The JSON carries only the fields the interface reads (id, title, timestamps and the
# turns), so it is not a second definition of the session schema. The turns' field names
# are Go's default marshalling of agent.DialogueTurn — capitalised, because that struct
# has no json tags — and a change there shows up immediately as a conversation that does
# not render at all, which the probe reports.
"$PY" - "$HOME_DIR/.motita/sessions" "$REPO/scripts/cdp_markdown.py" <<'PYSEED' || { echo "ERROR: the seed failed"; exit 2; }
import importlib.util, json, os, sys

out_dir, probe_path = sys.argv[1], sys.argv[2]
spec = importlib.util.spec_from_file_location("cdp_markdown", probe_path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

now = "2026-01-01T00:00:00Z"
record = {
    "id": "markdown",
    "title": "Markdown rendering",
    "created": now,
    "last_used": now,
    "turns": [{"User": "show me the markdown you render", "Agent": mod.EXAMPLE, "Kind": "chat"}],
}
with open(os.path.join(out_dir, "markdown.json"), "w") as f:
    json.dump(record, f)
print(f"    seeded {len(mod.EXAMPLE)} characters of Markdown")
PYSEED

HOME="$HOME_DIR" "$OUT/mockllm" -port "$PORT_LLM" -delay-ms 20 > "$OUT/mock.log" 2>&1 &
LLM_PID=$!
sleep 0.4
NO_COLOR=1 HOME="$HOME_DIR" MOTITA_LLM_API_KEY=test \
  "$OUT/motita" -config "$HOME_DIR/config.yaml" -serve \
  > "$OUT/gateway.log" 2>&1 &
GW_PID=$!

# The token file AND a 200 from /v1/health together are the readiness signal: the token
# is written before the bind, so a token alone is not enough, and an answering port alone
# is not enough.
TOKEN_FILE="$HOME_DIR/.motita/gateway.token"
deadline=$((SECONDS + 45))
while [ "$SECONDS" -lt "$deadline" ]; do
  if [ -s "$TOKEN_FILE" ] && curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if ! curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null; then
  echo "ERROR: the gateway never came up; its log holds:"
  sed -n 1,40p "$OUT/gateway.log" | sed 's/^/    /'
  exit 2
fi
ok "the gateway is answering on $BASE"

# The page must name the assets it needs, and the stylesheet must carry the rules this
# feature depends on. A class that emits no CSS is the failure mode this project has hit
# repeatedly (the Tailwind utilities it disables), so the served CSS is checked directly.
echo
echo "== 1. the page and the stylesheet it serves =="
page="$(curl -fsS "$BASE/")"
CSS_NAME="$(printf '%s' "$page" | grep -o 'assets/index-[A-Za-z0-9_-]*\.css' | head -1)"
JS_NAME="$(printf '%s' "$page" | grep -o 'assets/index-[A-Za-z0-9_-]*\.js' | head -1)"
if [ -n "$CSS_NAME" ] && [ -n "$JS_NAME" ]; then
  ok "the page names its own assets: $CSS_NAME, $JS_NAME"
else
  bad "the page names no hashed assets (CSS='$CSS_NAME' JS='$JS_NAME')"
fi

css="$(curl -fsS "$BASE/$CSS_NAME" 2>/dev/null)"
for c in markdown-body math-block math-inline mermaid-block render-error hljs-keyword \
         chat-spinner-wrap chat-spinner chat-spinner-label; do
  case "$css" in
    *".$c"*) ok "the served CSS defines .$c" ;;
    *) bad "the served CSS has no .$c: the element would render unstyled" ;;
  esac
done

# The lazy chunks must be reachable from the page's own HTML: if mermaid's chunk is
# eagerly imported the shell grows by ~700 KB, and if it is not reachable the diagram
# never draws. Both are checked by the probe below; here the entry bundle is checked for
# the API contract the whole interface rests on.
echo
echo "== 2. the entry bundle still does the session exchange =="
entry="$(curl -fsS "$BASE/$JS_NAME" 2>/dev/null)"
case "$entry" in
  *"/v1/webui/session"*) ok "the entry bundle exchanges the fragment for the cookie" ;;
  *) bad "the entry bundle never exchanges the fragment for the cookie" ;;
esac

echo
echo "== 3. the rendering, in a real browser =="
if ! curl -fsS -o /dev/null "http://127.0.0.1:$CDP_PORT/json/version"; then
  echo "  (starting chrome-headless-shell on port $CDP_PORT)"
  "$CHROME" --headless --remote-debugging-port="$CDP_PORT" \
    --user-data-dir="$OUT/cdp-profile" --no-sandbox --disable-gpu about:blank \
    > "$OUT/chrome.log" 2>&1 &
  CHROME_STARTED=1
  # -f, not -fsS: the probe is waiting for the browser to come up, and the connection
  # refused in the meantime is expected, not a problem to report.
  for _ in $(seq 1 40); do
    curl -sf -o /dev/null "http://127.0.0.1:$CDP_PORT/json/version" && break
    sleep 0.25
  done
fi
if [ ! -x "$PY" ]; then
  echo "  (creating the probe venv at $SCRATCH/.cdp)"
  uv venv "$SCRATCH/.cdp" --python /usr/bin/python3 >/dev/null 2>&1
  uv pip install --python "$PY" websockets pillow >/dev/null 2>&1
fi
export GATEWAY_URL="$BASE" GATEWAY_STATE="$TOKEN_FILE" CDP_PORT SHOTS_DIR="$OUT"
"$PY" "$REPO/scripts/cdp_markdown.py" > "$OUT/probe.out" 2>&1
probe_rc=$?
# The probe's own exit code IS the verdict: it counts its failed assertions and returns
# non-zero if there are any. A run that printed no VERDICT line crashed rather than
# judged, which is a failure of the check itself and must not pass silently.
sed -n '1,200p' "$OUT/probe.out" | sed 's/^/  /'
if grep -q '^VERDICT:' "$OUT/probe.out"; then
  [ "$probe_rc" = "0" ] || fail=$((fail+1))
else
  bad "the probe printed no verdict (exit $probe_rc); full output in $OUT/probe.out"
fi

echo
if [ "$fail" = "0" ]; then
  echo "VERDICT: the Markdown renders as it should - line breaks, escapes, inline HTML, footnotes, definition lists, formulas, diagrams, callouts and the copy buttons"
else
  echo "VERDICT: FAILED ($fail) - screenshots and the measurement are in $OUT"
fi
exit "$fail"
