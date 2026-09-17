#!/usr/bin/env bash
# End-to-end test of the i386 binary.
#
# It runs the binary compiled for linux/386 inside a real 32-bit container,
# against an OpenAI-compatible server (tools/mockapi) that also runs in there. It
# verifies the agent loop works: the "model" asks for tools, starlight runs them on
# the local system, returns the results and receives the final answer.
#
# Usage:  ./scripts/e2e-i386.sh
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGE="${IMAGE:-i386/debian:bookworm-slim}"
PORT="${PORT:-8099}"
MARKER="E2E-RESULT"

echo "==> Building the binaries for linux/386"
make 386 >/dev/null
mkdir -p dist
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -o dist/mockapi-linux-386 ./tools/mockapi
elf_class="$(head -c 5 dist/starlight-linux-386 | od -An -tx1 | tr -d ' \n')"
[ "$elf_class" = "7f454c4601" ] || { echo "ERROR: starlight-linux-386 is not ELFCLASS32"; exit 1; }
echo "    ELFCLASS32 confirmed ($elf_class)"

echo "==> Running inside $IMAGE (--platform linux/386)"
output="$(
  docker run --rm --platform linux/386 -v "$PWD/dist:/t:ro" "$IMAGE" sh -c "
    set -e
    echo \"architecture: \$(dpkg --print-architecture)\"
    /t/mockapi-linux-386 -port $PORT >/tmp/mock.log 2>&1 &
    # Active wait until the mock accepts requests.
    i=0
    while [ \$i -lt 50 ]; do
      if /t/starlight-linux-386 --version >/dev/null 2>&1 && \
         (exec 3<>/dev/tcp/127.0.0.1/$PORT) 2>/dev/null; then break; fi
      i=\$((i+1)); sleep 0.2
    done
    OPENAI_API_KEY=test \
    OPENAI_BASE_URL=http://127.0.0.1:$PORT/v1 \
    OPENAI_MODEL=mock \
    NO_COLOR=1 \
    /t/starlight-linux-386 -p 'tell me the architecture and the system version'
    echo '--- requests received by the mock ---'
    cat /tmp/mock.log
  "
)"
echo "$output"

echo
if grep -q "$MARKER" <<<"$output" && grep -q "architecture: i386" <<<"$output" && grep -q "i386" <<<"$output"; then
  echo "✅ E2E i386 OK: the 32-bit binary ran real tools and closed the agent loop."
else
  echo "❌ E2E i386 FAILED: the '$MARKER' marker or the expected architecture was not found."
  exit 1
fi
