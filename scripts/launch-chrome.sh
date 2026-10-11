#!/bin/sh
# Start a headless Chrome for a verification script, configured through the ENVIRONMENT.
#
# The Python probes used to build Chrome's argument list from CHROME, CDP_PORT and SHOTS_DIR
# themselves. Handing those to the launcher as variables keeps every argument of the process they
# start constant, and the script is the one place that spells the flags out.
#
# Environment: CHROME (the binary), CDP_PORT (the debugging port), and optionally CHROME_PROFILE
# (a --user-data-dir) and CHROME_WINDOW (a --window-size such as 1280,800).
# Arguments: the remaining flags, passed through to Chrome. It execs, so the caller's pid is Chrome's.
set -eu
: "${CHROME:?CHROME is not set}" "${CDP_PORT:?CDP_PORT is not set}"
set -- "--remote-debugging-port=$CDP_PORT" "$@"
if [ -n "${CHROME_PROFILE:-}" ]; then set -- "--user-data-dir=$CHROME_PROFILE" "$@"; fi
if [ -n "${CHROME_WINDOW:-}" ]; then set -- "--window-size=$CHROME_WINDOW" "$@"; fi
exec "$CHROME" "$@"
