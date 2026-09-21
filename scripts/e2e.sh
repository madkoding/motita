#!/usr/bin/env bash
# End-to-end test of the starlight binary on ONE target platform.
#
# It runs the binary compiled for that architecture inside a matching container,
# against an OpenAI-compatible server (tools/mockapi) that also runs in there. It
# verifies the agent loop works: the "model" asks for tools, starlight runs them on
# the local system, returns the results and receives the final answer.
#
# Usage:  ./scripts/e2e.sh <arch> [image]
#   ./scripts/e2e.sh 386
#   ./scripts/e2e.sh arm64     # needs qemu on the host
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
PORT="${PORT:-8099}"
MARKER="E2E-RESULT"

# The image name matters: Docker Hub only publishes some of these prefixes
# (386/, i386/, arm32v7/, arm64v8/, and the plain name for amd64).
case "$ARCH" in
  386)   [ -n "$IMAGE" ] || IMAGE="i386/debian:bookworm-slim" ;;
  amd64) [ -n "$IMAGE" ] || IMAGE="debian:bookworm-slim" ;;
  arm)   [ -n "$IMAGE" ] || IMAGE="arm32v7/debian:bookworm-slim" ;;
  arm64) [ -n "$IMAGE" ] || IMAGE="arm64v8/debian:bookworm-slim" ;;
  *) echo "ERROR: unknown architecture '$ARCH' (use 386, amd64, arm or arm64)"; exit 2 ;;
esac

case "$ARCH" in
  arm) PLATFORM="linux/arm/v7" ;;
  *)   PLATFORM="linux/$ARCH" ;;
esac

# The test binary MUST NOT be the released one. Both used to be built straight
# into dist/starlight-linux-<arch>, and the CI runs this script BEFORE uploading
# that path as the release artifact — so every published binary was the e2e build,
# stamped with version "e2e" instead of the tag. Everything this script builds now
# goes under dist/.e2e/, and it touches nothing else in dist/: a test that writes
# over the artifact it is meant to certify certifies something the user never gets.
E2E_DIST="dist/.e2e"
mkdir -p "$E2E_DIST"
TEST_BINARY="$E2E_DIST/starlight-linux-$ARCH"
TEST_MOCK="$E2E_DIST/mockapi-linux-$ARCH"

echo "==> Building the binaries for linux/$ARCH"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=e2e" -o "$TEST_BINARY" ./cmd/agent
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath -o "$TEST_MOCK" ./tools/mockapi

elf_class="$(head -c 5 "$TEST_BINARY" | od -An -tx1 | tr -d ' \n')"
case "$ARCH" in
  386|arm) want="7f454c4601" ;;
  *)       want="7f454c4602" ;;
esac
[ "$elf_class" = "$want" ] || { echo "ERROR: $TEST_BINARY is not the expected ELF class ($elf_class)"; exit 1; }
echo "    ELF class confirmed ($elf_class)"

echo "==> Running inside $IMAGE ($PLATFORM)"
output="$(
  docker run --rm --platform "$PLATFORM" -v "$PWD/$E2E_DIST:/t:ro" "$IMAGE" sh -c "
    set -e
    architecture=\$(dpkg --print-architecture)
    echo \"architecture: \$architecture\"
    /t/$(basename "$TEST_MOCK") -port $PORT >/tmp/mock.log 2>&1 &
    # Active wait until the mock accepts requests.
    i=0
    while [ \$i -lt 50 ]; do
      if /t/$(basename "$TEST_BINARY") --version >/dev/null 2>&1 && \
         (exec 3<>/dev/tcp/127.0.0.1/$PORT) 2>/dev/null; then break; fi
      i=\$((i+1)); sleep 0.2
    done
    OPENAI_API_KEY=test \
    OPENAI_BASE_URL=http://127.0.0.1:$PORT/v1 \
    OPENAI_MODEL=mock \
    NO_COLOR=1 \
    /t/$(basename "$TEST_BINARY") -p 'tell me the architecture and the system version'
    echo '--- requests received by the mock ---'
    cat /tmp/mock.log
  "
)"
echo "$output"

echo
if grep -q "$MARKER" <<<"$output" && grep -qi "architecture" <<<"$output"; then
  echo "✅ E2E on linux/$ARCH OK: the binary ran real tools and closed the agent loop."
else
  echo "❌ E2E on linux/$ARCH FAILED: the '$MARKER' marker was not found."
  exit 1
fi
