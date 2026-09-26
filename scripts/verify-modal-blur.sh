#!/usr/bin/env bash
#
# Reproduce and verify the modal backdrop blur, end to end, against the REAL
# gateway the user is running. Writes screenshots and a numeric verdict.
#
#   ./verify-modal-blur.sh [gateway-url] [cdp-port]
#
# Why the numbers and not just the screenshot: the backdrop blur of this UI was
# INVISIBLE for a while and no screenshot said so. The utility was emitted, the
# class was applied, the page looked plausible - only `getComputedStyle` showed
# `backdrop-filter: none`. What follows asserts the computed value AND that the
# pixels actually change when the declaration is removed, which is the part a
# screenshot cannot prove.
set -uo pipefail

BASE="${1:-http://127.0.0.1:7477}"
CDP_PORT="${2:-9334}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="${SCRATCH:-/home/madkoding/.hermes/cache/scratch}"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-modal-blur}"
PY="${PY:-$SCRATCH/.cdp/bin/python}"
CHROME="${CHROME:-$HOME/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell}"
mkdir -p "$OUT"

fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }

echo "== 1. the source of truth: the CSS the gateway serves =="
# The overlay's own class list is what decides. `backdrop-blur-sm` must exist in
# the served CSS WITH the variable defaults that make its declaration valid.
CSS_NAME="$(curl -fsS "$BASE/" | grep -o 'assets/index-[A-Za-z0-9_-]*\.css' | head -1)"
if [ -z "$CSS_NAME" ]; then
  bad "the page names no stylesheet"
else
  ok "stylesheet named by the page: $CSS_NAME"
  css="$(curl -fsS "$BASE/$CSS_NAME")"
  case "$css" in
    *"--tw-backdrop-brightness"*) ok "the --tw-backdrop-* defaults are emitted (the declaration is valid)" ;;
    *) bad "the --tw-backdrop-* defaults are MISSING: backdrop-filter resolves to none" ;;
  esac
  case "$css" in
    *"-webkit-backdrop-filter"*) ok "the -webkit- fallback is emitted" ;;
    *) bad "no -webkit-backdrop-filter fallback" ;;
  esac
  # A utility whose filter reads a variable that is never defined is a utility
  # that does nothing. This is the exact shape of the bug this script catches.
  missing="$(printf '%s' "$css" | grep -o 'var(--tw-[a-z-]*)' | sort -u \
    | while read -r v; do
        name="${v#var(}"; name="${name%)}"
        case "$name" in
          --tw-shadow-color) continue ;;   # set by box-shadow utilities, empty is valid
        esac
        printf '%s' "$css" | grep -q -- "$name:" || echo "$name"
      done)"
  if [ -z "$missing" ]; then ok "every var() a filter reads is defined somewhere"; \
  else bad "filters read undefined variables: $(echo "$missing" | tr '\n' ' ')"; fi
fi

echo
echo "== 2. the pixels: measured in a real browser =="
if ! curl -fsS -o /dev/null "$BASE/v1/health"; then
  bad "no gateway answering on $BASE"
else
  ok "gateway alive: $(curl -fsS "$BASE/v1/health")"
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
  # The probe lives in the repo, so the check does not depend on anything under
  # the scratch directory except its venv and its screenshots.
  export GATEWAY_URL="$BASE" CDP_PORT="$CDP_PORT" SHOTS_DIR="$OUT"
  if "$PY" "$REPO/scripts/cdp_modal_blur.py" > "$OUT/probe.out" 2>&1; then
    sed 's/^/  /' "$OUT/probe.out"
    blurred="$(grep -o 'lap_var WITH blur *: [0-9.]*' "$OUT/probe.out" | grep -o '[0-9.]*$')"
    control="$(grep -o 'lap_var control (blur off) *: [0-9.]*' "$OUT/probe.out" | grep -o '[0-9.]*$')"
    if [ -n "$blurred" ] && [ -n "$control" ]; then
      # The comparison is done inside awk, on the two lap_var values themselves:
      # routing it through a formatted ratio would run into the locale's decimal
      # comma (es_CL prints "10,18"), which awk then reads as the number 10.
      verdict="$(LC_ALL=C awk -v b="$blurred" -v c="$control" \
        'BEGIN { if (b > 0 && c / b >= 3) printf "PASS %.2f", c / b; else printf "FAIL %.2f", (b > 0 ? c / b : 0) }')"
      case "$verdict" in
        PASS*) ok "the backdrop is measurably SMOOTHER with the blur than without (ratio ${verdict#PASS }x)" ;;
        *) bad "toggling the blur barely changes the pixels (ratio ${verdict#FAIL }x): the sample or the declaration is wrong" ;;
      esac
    else
      bad "the probe printed no numbers"
    fi
  else
    bad "the browser probe failed; see $SCRATCH/verify-modal-blur.out"
  fi
fi

echo
if [ "$fail" = "0" ]; then echo "VERDICT: the modal backdrop really is blurred (light)"; \
else echo "VERDICT: FAILED ($fail)"; fi
exit "$fail"
