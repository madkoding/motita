#!/usr/bin/env bash
# End-to-end test of the 3-layer agent on real 32-bit.
#
# Inside a linux/386 container:
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
# Usage:  ./scripts/e2e-agent-i386.sh
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGEN="${IMAGEN:-i386/debian:bookworm-slim}"
PUERTO="${PUERTO:-8210}"

echo "==> Building for linux/386 (agent + simulated LLM)"
make agent-386 >/dev/null
mkdir -p dist
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -o dist/mockllm-linux-386 ./tools/mockllm
clase="$(head -c 5 dist/starlight-agent-386 | od -An -tx1 | tr -d ' \n')"
[ "$clase" = "7f454c4601" ] || { echo "ERROR: the agent is not ELFCLASS32"; exit 1; }
echo "    ELFCLASS32 confirmed ($clase)"

echo "==> Preparing the scenario"
rm -rf .e2e && mkdir -p .e2e/work
cp configs/e2e-agent.yaml .e2e/config.yaml
cp configs/e2e-task.txt .e2e/task.txt

output="$(
  docker run --rm --platform linux/386 \
    -v "$PWD/dist:/dist:ro" \
    -v "$PWD/.e2e:/e2e" \
    -w /e2e \
    "$IMAGEN" sh -c "
    set -e
    echo \"architecture: \$(dpkg --print-architecture)\"
    /dist/mockllm-linux-386 -port $PUERTO >/e2e/mock.log 2>&1 &
    i=0
    while [ \$i -lt 50 ]; do
      (exec 3<>/dev/tcp/127.0.0.1/$PUERTO) 2>/dev/null && break
      i=\$((i+1)); sleep 0.2
    done
    NO_COLOR=1 STARLIGHT_LLM_API_KEY=test \
      /dist/starlight-agent-386 -config /e2e/config.yaml 2>&1 || echo \"[the agent exited with \$?]\"
    echo '--- requests received by the simulated LLM ---'
    cat /e2e/mock.log
  "
)"
echo "$output"
echo

echo "==> Checking the result on the filesystem"
fallos=0
comprobar() {
  if [ -e "$1" ]; then echo "  ok  $2"; else echo "  MISSING: $2 ($1)"; fallos=$((fallos+1)); fi
}
comprobar .e2e/work/report.txt "the final attempt's command created the report"
comprobar .e2e/work/final-action.txt "the final action ran after PASS"

if [ -f .e2e/work/report.txt ]; then
  contenido="$(cat .e2e/work/report.txt)"
  if [ "$contenido" = "content-valid" ]; then
    echo "  ok  the report holds the requested contents: $contenido"
  else
    echo "  UNEXPECTED contents in the report: $contenido"
    fallos=$((fallos+1))
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
if [ "$fallos" -eq 0 ]; then
  echo "E2E of the 3-layer agent on i386: OK"
else
  echo "E2E FAILED with $fallos check(s)"
  exit 1
fi
