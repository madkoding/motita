#!/usr/bin/env bash
# Prove the android build survives Android's seccomp policy, and the linux build does not.
#
# On Android an app's seccomp policy kills the process (SIGSYS) on faccessat2, which the Go
# linux runtime calls from os/exec and the android runtime avoids. Here tools/seccomptrap
# installs that one rule and then runs each build inside termux-docker (a real Android
# userland), on the path that crashed in Termux: `motita config`, which looks up stty.
#
# The linux build is the control: if it does NOT die, the trap is not reproducing Android
# and a pass for the android build would mean nothing.
#
# It needs a NATIVE arm64 host: qemu-user refuses seccomp filters (PR_SET_SECCOMP returns
# EINVAL), and a filter on runc itself cannot work, because runc looks the program up
# with the very syscall being trapped.
#
# Exit codes: 0 the android build survives and the linux build dies · 1 anything else.
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE="${TERMUX_IMAGE:-termux/termux-docker:aarch64}"
OUT="$(mktemp -d "${TMPDIR:-/tmp}/motita-seccomp.XXXXXX")"
trap 'rm -rf "$OUT"' EXIT

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(uname -m)" = aarch64 ] || fail "needs a native arm64 host (this one is $(uname -m))"

export CGO_ENABLED=0 GOARCH=arm64
GOOS=linux   go build -trimpath -o "$OUT/seccomptrap" ./tools/seccomptrap
GOOS=linux   go build -trimpath -o "$OUT/motita-linux"   ./cmd/agent
GOOS=android go build -trimpath -o "$OUT/motita-android" ./cmd/agent
chmod -R a+rX "$OUT"

# -t gives `config` a terminal, so it reaches the stty lookup, which is where Termux
# crashed. A build that crashes does so at once; one that survives sits at the wizard's
# first prompt until the timeout ends it, and the timeout is counted as surviving.
run() {
  local name="motita-seccomp-$1-$$"
  timeout 30 docker run --rm -t --name "$name" -v "$OUT:/t:ro" "$IMAGE" \
    sh -c "/t/seccomptrap /t/$1 config </dev/null; echo \"exit=\$?\"" 2>&1 || true
  docker rm -f "$name" >/dev/null 2>&1 || true
}

# The trap itself must be in place, or both results below are meaningless.
out="$(run motita-linux)"
printf '%s\n' "$out" | grep -q 'seccomptrap:' && fail "the seccomp rule could not be installed: $out"
printf '%s\n' "$out" | grep -q 'SIGSYS' || fail "the linux build did not die under the trap, so the trap does not reproduce Android: $out"
echo "control: the linux build dies with SIGSYS under the Android rule"

out="$(run motita-android)"
printf '%s\n' "$out" | grep -q 'SIGSYS' && fail "the android build died with SIGSYS: $out"
printf '%s\n' "$out" | grep -q 'seccomptrap:' && fail "the android build could not be started: $out"
# The wizard prints its first question only after the stty lookup, so seeing it proves
# the build got past the call that crashed, rather than failing some other way.
printf '%s\n' "$out" | grep -q 'Which AI provider' || fail "the android build did not reach the wizard: $out"
echo "the android build runs \`motita config\` under the Android rule"

echo "VERDICT: the android build survives Android's seccomp policy"
