#!/usr/bin/env bash
#
# Measure the site's text contrast against its BACKGROUND IMAGE, in a real
# browser, with the rendered pixels.
#
#   ./verify-site-contrast.sh [site-url]
#
# Why a separate check from audit-page.mjs: that auditor - like every
# ancestor-walking contrast checker - stops at the first opaque background, which
# on this site is body's #0a0a0f. The illustration is painted over it, so the
# auditor reports a comfortable pass against a colour nobody sees. Measured here:
# the muted text read 4.9:1 by the audit and 2.05:1 against the actual pixels,
# which is a page that looks atmospheric in a screenshot and is genuinely hard to
# read.
#
# Usage: ./verify-site-contrast.sh [url]
#   Default url serves site/ on a port this script owns and stops afterwards.
#   Pass a URL to measure a running server instead.
#
# Exit codes: 0 measured and passing · 1 a ratio failed · 2 cannot run (skipped).
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRATCH="${SCRATCH:-/home/madkoding/.hermes/cache/scratch}"
OUT="${OUT:-${TMPDIR:-/tmp}/motita-site-contrast}"
PY="${PY:-$SCRATCH/.cdp/bin/python}"
CHROME="${CHROME:-$HOME/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell}"
CDP_PORT="${CDP_PORT:-9356}"
PORT="${PORT:-8799}"
WIDTH="${SITE_WIDTH:-1440}"
mkdir -p "$OUT"

# A machine without the browser, or without the probe venv, is a MISSING TOOL and
# not a defect in the site. Exit 2 is the convention verify.sh reads as "skipped"
# (the same one verify-spinner.sh and check-binary-size.sh use), so a contributor
# who lacks either gets an honest skip instead of a red gate.
if [ ! -x "$CHROME" ]; then
  echo "SKIP: no chrome-headless-shell at $CHROME"
  echo "      set CHROME=... to the one on this machine"
  exit 2
fi
if [ ! -x "$PY" ]; then
  echo "  (creating the probe venv at $SCRATCH/.cdp)"
  uv venv "$SCRATCH/.cdp" --python /usr/bin/python3 >/dev/null 2>&1 || true
  uv pip install --python "$PY" websockets pillow >/dev/null 2>&1 || true
fi
if [ ! -x "$PY" ]; then
  echo "SKIP: could not create the probe venv (websockets + pillow needed)"
  exit 2
fi

# The page is served over HTTP rather than measured from file://: the harness
# must load exactly what a reader loads, and file:// changes how a browser treats
# origins and caching.
SERVER_PID=""
SITE_URL="${1:-}"
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  return 0
}
trap cleanup EXIT

if [ -z "$SITE_URL" ]; then
  (cd "$REPO/site" && exec python3 -m http.server "$PORT" --bind 127.0.0.1) >"$OUT/httpd.log" 2>&1 &
  SERVER_PID=$!
  SITE_URL="http://127.0.0.1:$PORT/index.html"
  for _ in $(seq 1 40); do
    curl -sf -o /dev/null "$SITE_URL" && break
    sleep 0.25
  done
  if ! curl -sf -o /dev/null "$SITE_URL"; then
    echo "ERROR: could not serve $REPO/site on port $PORT"
    exit 2
  fi
fi

echo "==> measuring $SITE_URL at ${WIDTH}px"
export CDP_PORT CHROME SITE_URL SHOTS_DIR="$OUT" SITE_WIDTH="$WIDTH"
"$PY" "$REPO/scripts/cdp_site_contrast.py"
rc=$?
if [ "$rc" = "0" ]; then
  echo "VERDICT: the site's text is legible over its own background image"
elif [ "$rc" = "2" ]; then
  echo "SKIP: the probe could not run"
fi
exit "$rc"
