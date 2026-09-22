#!/bin/sh
# Is the Go floor in go.mod still a release Go supports?
#
#   scripts/check-go-floor.sh
#
# Exit codes, so each caller can decide what "could not check" means:
#   0  the floor is supported
#   1  the floor is no longer supported (the caller must fail)
#   2  the check could not run (no network, or curl is missing)
#
# Why this exists: the standard library is compiled INTO the published binary, so a
# toolchain past its support window hands its known vulnerabilities to whoever downloads
# it. Go keeps a release for two newer majors and then stops patching it, which is how this
# project was shipping a binary whose stdlib carried 26 reachable vulnerabilities while
# go.mod still said `go 1.23`.
#
# go.dev/dl?mode=json lists exactly the releases Go still supports, so the question is
# asked directly instead of being kept as a date somebody has to remember to bump.
#
# GO_DL_JSON, if set, replaces the endpoint. Used by the tests to drive the three
# outcomes without a network, and available for pointing at a mirror.
set -u

cd "$(dirname "$0")/.."

URL="${GO_DL_JSON:-https://go.dev/dl/?mode=json}"

# "go 1.26" -> "26". The 1. prefix is fixed: Go 2 does not exist.
floor="$(grep -oE '^go 1\.[0-9]+' go.mod | head -1 | sed 's/^go 1\.//')"
if [ -z "$floor" ]; then
	echo "go.mod does not state a Go version (expected a line like: go 1.26)" >&2
	exit 1
fi

# No jq on the target machine: one record per line, keep the series, drop the patch.
supported="$(curl -fsSL --connect-timeout 10 "$URL" 2>/dev/null \
	| tr '{' '\n' | grep -oE '"version":[[:space:]]*"go1\.[0-9]+' \
	| grep -oE 'go1\.[0-9]+' | sed 's/^go1\.//' | sort -un)"

if [ -z "$supported" ]; then
	echo "could not read the supported releases from $URL" >&2
	exit 2
fi

if echo "$supported" | grep -qx "$floor"; then
	echo "go.mod asks for go 1.${floor}; still supported (1.$(echo "$supported" | tr '\n' ' ' | sed 's/ $//;s/ /, 1./g'))"
	exit 0
fi

echo "go.mod asks for go 1.${floor}, which is no longer supported (newest series: 1.$(echo "$supported" | tail -1))." >&2
echo "Raise the floor in go.mod and REFERENCE.md, then re-run govulncheck." >&2
exit 1
