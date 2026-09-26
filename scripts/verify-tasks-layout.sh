#!/usr/bin/env bash
#
# Verify the scheduled-tasks panel against a RUNNING gateway, end to end, with a
# real browser and a seeded set of tasks. Writes screenshots and a numeric verdict.
#
#   ./verify-tasks-layout.sh [gateway-url] [cdp-port]
#
# The gateway it points at must have a schedule directory with tasks in it: the
# check is about how a row READS and how the panel is ARRANGED, so an empty list
# would prove nothing. It reads the token from the gateway's service file
# ($GATEWAY_STATE), which is how the page authenticates.
set -uo pipefail

BASE="${1:-http://127.0.0.1:7479}"
CDP_PORT="${2:-9344}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="${SCRATCH:-/home/madkoding/.hermes/cache/scratch}"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-tasks-layout}"
PY="${PY:-$SCRATCH/.cdp/bin/python}"
CHROME="${CHROME:-$HOME/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell}"
GATEWAY_STATE="${GATEWAY_STATE:-$SCRATCH/motita-twocol/.motita/gateway.json}"
mkdir -p "$OUT"

fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }

echo "== 1. the served stylesheet carries the panel's own classes =="
CSS_NAME="$(curl -fsS "$BASE/" | grep -o 'assets/index-[A-Za-z0-9_-]*\.css' | head -1)"
if [ -z "$CSS_NAME" ]; then
  bad "the page names no stylesheet"
else
  ok "stylesheet named by the page: $CSS_NAME"
  css="$(curl -fsS "$BASE/$CSS_NAME")"
  # The classes are plain CSS in index.css precisely because the Tailwind
  # utilities this project disables emit nothing - so their PRESENCE in the
  # served file is the first thing to check, not an assumption.
  for c in tasks-panel tasks-body tasks-form tasks-list-column tasks-list task-tag task-countdown; do
    case "$css" in
      *".$c"*) ok "served CSS defines .$c" ;;
      *) bad "served CSS has no .$c" ;;
    esac
  done
  case "$css" in
    *"grid-template-columns:minmax(0,1fr) minmax(0,1.25fr)"*|*"grid-template-columns: minmax(0, 1fr) minmax(0, 1.25fr)"*)
      ok "the desktop grid columns are emitted" ;;
    *) bad "the desktop grid columns are missing from the served CSS" ;;
  esac
  # The desktop rules live inside a min-width media query: a grid declared
  # outside one would apply to the phone too and break the single column.
  if printf '%s' "$css" | grep -q 'min-width:768px\|min-width: 768px'; then
    ok "the two-column rules sit behind a min-width media query"
  else
    bad "no min-width media query around the two-column rules"
  fi
fi

echo
echo "== 2. the layout in a real browser, at two widths =="
if ! curl -fsS -o /dev/null "$BASE/v1/health"; then
  bad "no gateway answering on $BASE"
else
  tasks="$(curl -fsS -H "Authorization: Bearer $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["token"])' "$GATEWAY_STATE")" "$BASE/v1/schedules")"
  n="$(printf '%s' "$tasks" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["schedules"]))')"
  if [ "$n" -ge 2 ]; then ok "the gateway holds $n tasks to measure"
  else bad "only $n task(s): seed the fixture first, an empty list proves nothing"; fi
  if ! curl -fsS -o /dev/null "http://127.0.0.1:$CDP_PORT/json/version"; then
    echo "  (starting chrome-headless-shell on port $CDP_PORT)"
    "$CHROME" --headless --remote-debugging-port="$CDP_PORT" \
      --user-data-dir="$OUT/cdp-profile" --no-sandbox --disable-gpu about:blank \
      >"$OUT/chrome.log" 2>&1 &
    for _ in $(seq 1 40); do
      curl -fsS -o /dev/null "http://127.0.0.1:$CDP_PORT/json/version" && break
      sleep 0.25
    done
  fi
  if [ ! -x "$PY" ]; then
    echo "  (creating the probe venv at $SCRATCH/.cdp)"
    uv venv "$SCRATCH/.cdp" --python /usr/bin/python3 >/dev/null 2>&1
    uv pip install --python "$PY" websockets pillow >/dev/null 2>&1
  fi
  export GATEWAY_URL="$BASE" CDP_PORT="$CDP_PORT" SHOTS_DIR="$OUT" GATEWAY_STATE
  "$PY" "$REPO/scripts/cdp_tasks_layout.py" > "$OUT/probe.out" 2>&1
  probe_rc=$?
  # The probe prints its own per-assertion lines; they are re-emitted here so the
  # whole verdict reads as one report. Only the assertions count towards the exit
  # code, and a probe that printed none of them crashed rather than judged.
  if grep -qE '^  (ok|FAIL)' "$OUT/probe.out"; then
    grep -E '^  (ok|FAIL)' "$OUT/probe.out" | sed 's/^/  /'
    fails="$(grep -c '^  FAIL' "$OUT/probe.out")"
    if [ "$fails" != "0" ]; then fail=$((fail+1)); fi
  else
    sed -n '1,60p' "$OUT/probe.out" | sed 's/^/  /'
    bad "the probe printed no assertions (exit $probe_rc); full output in $OUT/probe.out"
  fi
fi

echo
if [ "$fail" = "0" ]; then echo "VERDICT: the tasks panel is two columns on a desktop and one on a phone"; \
else echo "VERDICT: FAILED ($fail)"; fi
exit "$fail"
