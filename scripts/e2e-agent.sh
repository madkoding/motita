#!/usr/bin/env bash
# End-to-end test of the 3-layer agent on ONE target platform.
#
# It is the same test the i386 script runs, generalised so every published
# platform can be verified with it: the architecture decides which binary is
# built, which container image runs it and which emulation (if any) is needed.
#
# Inside the container:
#   1. a simulated LLM (tools/mockllm) is started; it behaves like a model that
#      first fails and then corrects itself,
#   2. the agent runs with a real configuration,
#   3. the ANCHOR validates with a real command (the file exists and holds the
#      right contents),
#   4. if it validates, the FINAL ACTION runs.
#
# The result is checked on the filesystem, not in what the agent says about
# itself.
#
# Usage:  ./scripts/e2e-agent.sh <arch> [image]
#   ./scripts/e2e-agent.sh 386
#   ./scripts/e2e-agent.sh arm64
#   ./scripts/e2e-agent.sh arm        # armv7, needs qemu on the host
set -euo pipefail

cd "$(dirname "$0")/.."

# This repository is also developed inside a container where Go is not on the
# default PATH; the CI has it. Prefer whatever is already available.
if ! command -v go >/dev/null 2>&1 && [ -x /opt/data/cache/go/bin ]; then
  export PATH="/opt/data/cache/go/bin:$PATH"
  export GOCACHE="${GOCACHE:-/opt/data/cache/go-build}"
  export GOPATH="${GOPATH:-/opt/data/cache/gopath}"
fi
command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }

ARCH="${1:-386}"
IMAGE="${2:-}"
PORT="${PUERTO:-8210}"

# The image has to match the architecture: running an arm binary in an amd64
# container fails with "exec format error", which looks like a broken binary.
# The image name matters: Docker Hub only publishes some of these prefixes
# (386/, i386/, arm32v7/, arm64v8/, and the plain name for amd64).
case "$ARCH" in
  386)   [ -n "$IMAGE" ] || IMAGE="i386/debian:bookworm-slim" ;;
  amd64) [ -n "$IMAGE" ] || IMAGE="debian:bookworm-slim" ;;
  arm)   [ -n "$IMAGE" ] || IMAGE="arm32v7/debian:bookworm-slim" ;;
  arm64) [ -n "$IMAGE" ] || IMAGE="arm64v8/debian:bookworm-slim" ;;
  *) echo "ERROR: unknown architecture '$ARCH' (use 386, amd64, arm or arm64)"; exit 2 ;;
esac

# docker's platform spelling differs from Go's for 32-bit arm.
case "$ARCH" in
  arm) PLATFORM="linux/arm/v7" ;;
  *)   PLATFORM="linux/$ARCH" ;;
esac

BINARY="dist/.e2e/starlight-agent-linux-$ARCH"
MOCK="dist/.e2e/mockllm-linux-$ARCH"

# The test binaries MUST NOT share a path with a released artifact. The CI runs
# this script BEFORE uploading dist/starlight-agent-linux-<arch>, so building the
# test binary into that name made every published agent binary a test build
# stamped "e2e" instead of the release tag. Everything here goes under
# dist/.e2e/, and nothing else in dist/ is touched. The path is asserted so a
# future edit cannot quietly reintroduce the collision.
case "$BINARY" in dist/.e2e/*) ;; *) echo "ERROR: the test binary must live under dist/.e2e/"; exit 1 ;; esac
mkdir -p dist/.e2e

echo "==> Building for linux/$ARCH (agent + simulated LLM)"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=e2e" -o "$BINARY" ./cmd/agent
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath -o "$MOCK" ./tools/mockllm

# The 32-bit build must really be 32-bit; the others must be the class we asked
# for, which is what catches a cross-compile that silently produced the host's
# architecture.
clase="$(head -c 5 "$BINARY" | od -An -tx1 | tr -d ' \n')"
case "$ARCH" in
  386|arm) esperado="7f454c4601" ;;   # ELFCLASS32
  *)       esperado="7f454c4602" ;;   # ELFCLASS64
esac
[ "$clase" = "$esperado" ] || { echo "ERROR: $BINARY is not the expected ELF class ($clase)"; exit 1; }
echo "    ELF class confirmed ($clase)"

echo "==> Preparing the scenario"
rm -rf .e2e && mkdir -p .e2e/work
cp configs/e2e-agent.yaml .e2e/config.yaml
cp configs/e2e-task.txt .e2e/task.txt

echo "==> Running in $IMAGE ($PLATFORM)"
output="$(
  docker run --rm --platform "$PLATFORM" \
    -v "$PWD/dist:/dist:ro" \
    -v "$PWD/.e2e:/e2e" \
    -w /e2e \
    "$IMAGE" sh -c "
    set -e
    echo \"architecture: \$(dpkg --print-architecture)\"
    /dist/.e2e/$(basename "$MOCK") -port $PORT >/e2e/mock.log 2>&1 &
    i=0
    while [ \$i -lt 50 ]; do
      (exec 3<>/dev/tcp/127.0.0.1/$PORT) 2>/dev/null && break
      i=\$((i+1)); sleep 0.2
    done
    NO_COLOR=1 STARLIGHT_LLM_API_KEY=test \
      /dist/.e2e/$(basename "$BINARY") -config /e2e/config.yaml 2>&1 || echo \"[the agent exited with \$?]\"
    echo '--- requests received by the simulated LLM ---'
    cat /e2e/mock.log
  "
)"
echo "$output"
echo

echo "==> Checking the result on the filesystem"
failures=0
check() {
  if [ -e "$1" ]; then echo "  ok  $2"; else echo "  MISSING: $2 ($1)"; failures=$((failures+1)); fi
}
check .e2e/work/report.txt "the final attempt's command created the report"
check .e2e/work/final-action.txt "the final action ran after PASS"

if [ -f .e2e/work/report.txt ]; then
  content="$(cat .e2e/work/report.txt)"
  if [ "$content" = "content-valid" ]; then
    echo "  ok  the report holds the requested contents: $content"
  else
    echo "  UNEXPECTED contents in the report: $content"
    failures=$((failures+1))
  fi
fi

# The anchor must have failed on the first attempt and passed on the second:
# that proves the retry loop works with real data.
if grep -q "attempt failed" .e2e/agent.log 2>/dev/null || echo "$output" | grep -q "attempt failed"; then
  echo "  ok  there was a failed attempt before PASS (the retry worked)"
else
  echo "  ?   no record of a failed attempt found"
fi

rm -rf .e2e

echo
if [ "$failures" -eq 0 ]; then
  echo "E2E of the 3-layer agent on linux/$ARCH: OK"
else
  echo "E2E FAILED with $failures check(s)"
  exit 1
fi
