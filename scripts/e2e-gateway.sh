#!/usr/bin/env bash
# End-to-end test of the gateway on ONE target platform.
#
# The real binary runs in a matching container against the simulated LLM the other e2e script
# uses, and is driven ENTIRELY over HTTP by a client outside it twice over: outside the process
# (curl, not an injected Go client) and outside the container (the host, over loopback). That is
# the claim being tested - a client that is not the process can drive the agent - so driving it
# any closer would not be testing it.
#
# The result is checked IN THE HTTP RESPONSES and ON THE FILESYSTEM, never in what the program
# says about itself.
#
# Two deliberate choices about the container:
#
#   --network host  because a debian-slim image ships no HTTP client and the client has to be the
#                   host. With the host network namespace, 127.0.0.1 inside IS 127.0.0.1 outside,
#                   so gateway.allow_lan stays false and the gateway is still bound to loopback -
#                   the configuration being tested. Publishing a port from 0.0.0.0 would have
#                   meant testing a LAN-exposed gateway instead, which is a different thing.
#   --user          so the token file is owned by the user running the test. It is created mode
#                   0600, which is a property worth keeping rather than relaxing for a test, and
#                   a root-owned 0600 file in the mounted directory is unreadable here.
#
# Usage:  ./scripts/e2e-gateway.sh [arch] [image]
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v go >/dev/null 2>&1 && [ -x /home/madkoding/.hermes/cache/go/bin ]; then
  export PATH="/home/madkoding/.hermes/cache/go/bin:$PATH"
  export GOCACHE="${GOCACHE:-/home/madkoding/.hermes/cache/go-build}"
  export GOPATH="${GOPATH:-/home/madkoding/.hermes/cache/gopath}"
fi
command -v go >/dev/null 2>&1 || { echo "ERROR: go is not on the PATH"; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl is not on the PATH"; exit 2; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: docker is not on the PATH"; exit 2; }

ARCH="${1:-amd64}"
IMAGE="${2:-}"
# The LLM port is 8210 because configs/e2e-agent.yaml points llm.base_url at it: the container
# writes the port it was given here and the configuration names it, so the two have to agree.
PORT_LLM="${PORT_LLM:-8210}"
PORT_GW="${PORT_GW:-8321}"
BASE="http://127.0.0.1:$PORT_GW"
CONTAINER="starlight-gateway-e2e-$ARCH"

case "$ARCH" in
  amd64) [ -n "$IMAGE" ] || IMAGE="debian:bookworm-slim";;
  arm64) [ -n "$IMAGE" ] || IMAGE="arm64v8/debian:bookworm-slim";;
  386)   [ -n "$IMAGE" ] || IMAGE="i386/debian:bookworm-slim";;
  *) echo "ERROR: unknown architecture '$ARCH'"; exit 2;;
esac
case "$ARCH" in
  arm) PLATFORM="linux/arm/v7";;
  *)   PLATFORM="linux/$ARCH";;
esac

BINARY="dist/.e2e/starlight-gateway-linux-$ARCH"
MOCK="dist/.e2e/mockllm-gateway-linux-$ARCH"
case "$BINARY" in dist/.e2e/*) ;; *) echo "ERROR: the test binary must live under dist/.e2e/"; exit 1;; esac
mkdir -p dist/.e2e

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> Building for linux/$ARCH (agent + simulated LLM)"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=e2e" -o "$BINARY" ./cmd/agent
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath -o "$MOCK" ./tools/mockllm

elf_class="$(head -c 5 "$BINARY" | od -An -tx1 | tr -d ' \n')"
case "$ARCH" in
  386|arm) want_class="7f454c4601";;
  *)       want_class="7f454c4602";;
esac
[ "$elf_class" = "$want_class" ] || { echo "ERROR: $BINARY is not the expected ELF class ($elf_class)"; exit 1; }
echo "    ELF class confirmed ($elf_class)"

rm -rf .e2e && mkdir -p .e2e/work
cp configs/e2e-agent.yaml .e2e/config.yaml
cp configs/e2e-task.txt .e2e/task.txt

echo "==> Starting $IMAGE ($PLATFORM) on the host network"
docker run -d --name "$CONTAINER" --platform "$PLATFORM" \
  --network host \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/dist:/dist:ro" \
  -v "$PWD/.e2e:/e2e" \
  -w /e2e \
  "$IMAGE" sh -c "
    /dist/.e2e/$(basename "$MOCK") -port $PORT_LLM >/e2e/mock.log 2>&1 &
    NO_COLOR=1 STARLIGHT_LLM_API_KEY=test \
      /dist/.e2e/$(basename "$BINARY") -config /e2e/config.yaml -serve \
        -gateway 127.0.0.1:$PORT_GW >/e2e/gateway.log 2>&1 &
    wait
  " >/dev/null

# The token file AND a 200 from /v1/health together are the readiness signal: the token is written
# before the bind, so a token alone is not enough, and an answering port alone is not enough.
echo "==> Waiting for the gateway"
deadline=$((SECONDS + 60))
while [ "$SECONDS" -lt "$deadline" ]; do
  if [ -s .e2e/gateway.token ] && curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if ! curl -fsS -o /dev/null "$BASE/v1/health" 2>/dev/null; then
  echo "ERROR: the gateway never came up; its log holds:"
  cat .e2e/gateway.log 2>/dev/null || true
  exit 1
fi
echo "    the gateway is answering on $BASE"

TOKEN="$(cat .e2e/gateway.token)"
AUTH="Authorization: Bearer $TOKEN"

echo
echo "--- 1. health answers with no token ---"
health_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/health")"
echo "$health_code"

echo "--- 2. config without a token ---"
noauth_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/config")"
echo "$noauth_code"

echo "--- 3. config with the token ---"
config_body="$(curl -s -H "$AUTH" "$BASE/v1/config")"
echo "$config_body"

echo "--- 4. a streamed run ---"
run_body="$(
  curl -s -N -X POST -H "$AUTH" -H 'Content-Type: application/json' \
    -d '{"task":"leave the report with the requested content"}' \
    "$BASE/v1/task"
)"
echo "$run_body" | head -40

echo "--- 5. the slot is free again and the conversation is readable ---"
session_code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/session")"
echo "$session_code"

echo "--- requests received by the simulated LLM ---"
cat .e2e/mock.log 2>/dev/null || true
echo

echo "==> Checking the result over HTTP and on the filesystem"
failures=0
ok()  { echo "  ok    $*"; }
bad() { echo "  FAIL  $*"; failures=$((failures+1)); }

[ "$health_code" = "200" ] && ok "health answered 200 without a token" \
  || bad "health answered $health_code without a token, want 200"
[ "$noauth_code" = "401" ] && ok "config refused an unauthenticated request" \
  || bad "config answered $noauth_code without a token, want 401"
case "$config_body" in
  *'"provider"'*) ok "config answered the authenticated request" ;;
  *) bad "config did not answer the authenticated request: $config_body" ;;
esac
# The API key must never cross this wire. This scans every response body collected above, which
# makes it a check of the RESPONSES rather than of the code that writes them.
if printf '%s\n%s\n' "$config_body" "$run_body" | grep -q 'sk-'; then
  bad "an API key appeared in a response"
else
  ok "no API key in any response"
fi

case "$run_body" in
  *'event: done'*)     ok "the run ended with a done event" ;;
  *) bad "the run did not end with a done event" ;;
esac
case "$run_body" in
  *'event: progress'*) ok "the run streamed progress" ;;
  *) bad "the run streamed no progress" ;;
esac
[ "$session_code" = "200" ] && ok "the slot was free for the next request" \
  || bad "the session read answered $session_code after the run, want 200"

[ -f .e2e/work/report.txt ] && ok "the run left report.txt behind" \
  || bad "the run left no report.txt"
if [ -f .e2e/work/report.txt ]; then
  content="$(cat .e2e/work/report.txt)"
  [ "$content" = "content-valid" ] && ok "the report holds the requested contents" \
    || bad "the report holds '$content'"
fi

[ "$failures" -eq 0 ] || { echo "FAILURES: $failures"; exit 1; }
echo "the gateway works end to end"

