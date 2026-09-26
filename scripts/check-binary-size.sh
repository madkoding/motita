#!/bin/sh
# Does a built binary stay under the project's size ceiling?
#
#   scripts/check-binary-size.sh <path-to-binary> [path...]
#
# Exit codes, so each caller can decide what "could not check" means:
#   0  every binary is under the ceiling
#   1  at least one is over it (the caller must fail)
#   2  the check could not run (no argument, or a path that is not a file)
#
# ONE implementation, two callers - this script from scripts/verify.sh, and a
# literal 20971520 in .github/workflows/ci.yml. The CI copies the number because
# it measures binaries built by a matrix step on the runner, where invoking a
# repo script for a two-line comparison is more machinery than the check is
# worth. The risk of two copies is that they drift, so this is the number that
# MOVES and CI is what has to be kept in step; the ceiling is stated in
# CONTRIBUTING.md and both readers.
#
# Why the ceiling exists, and what it is NOT: it guards against runaway growth -
# a dependency that drags a framework into the binary the user downloads - not
# against a feature. Features land in this same binary (the web interface, the
# WebSocket transport, scheduled tasks), and a per-feature budget would have made
# each of them pay a tax for work that belongs there. It was 10 MB while the
# project was a CLI; at 20 MB it still catches a framework (React instead of
# Preact, a WebSocket library, an ORM) while leaving room to keep building.
set -u

CEILING=${BINARY_CEILING:-20971520}
LIMIT_MB=$((CEILING / 1024 / 1024))

if [ "$#" -eq 0 ]; then
	echo "usage: scripts/check-binary-size.sh <binary> [binary...]" >&2
	exit 2
fi

over=0
checked=0
for bin in "$@"; do
	[ -f "$bin" ] || continue
	bytes="$(stat -c%s "$bin" 2>/dev/null || stat -f%z "$bin" 2>/dev/null)"
	[ -n "$bytes" ] || { echo "could not measure $bin" >&2; exit 2; }
	checked=$((checked + 1))
	if [ "$bytes" -ge "$CEILING" ]; then
		echo "$bin weighs $bytes bytes, at or over the ${LIMIT_MB} MB ceiling ($CEILING)" >&2
		over=$((over + 1))
	else
		# Reported per binary, like the CI matrix does: a reader looking at a
		# failing run wants the number for the binary that failed, and a reader
		# looking at a passing one wants to see the headroom before it is gone.
		printf '%s: %s bytes (< %s MB, %s bytes of headroom)\n' \
			"$bin" "$bytes" "$LIMIT_MB" "$((CEILING - bytes))"
	fi
done

if [ "$checked" -eq 0 ]; then
	echo "none of the given paths is a file" >&2
	exit 2
fi

if [ "$over" -ne 0 ]; then
	echo "the ceiling is not the point: what grew? A new dependency in go.mod, or an asset" >&2
	echo "embedded through go:embed (internal/webui/assets) counts too." >&2
	exit 1
fi
exit 0
