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
    /dist/.e2e/$(basename "$MOCK") -port $PORT_LLM -delay-ms ${MOCK_DELAY_MS:-40} >/e2e/mock.log 2>&1 &
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
noauth_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/sessions/default/config")"
echo "$noauth_code"

echo "--- 3. config with the token ---"
config_body="$(curl -s -H "$AUTH" "$BASE/v1/sessions/default/config")"
echo "$config_body"

echo "--- 4. a streamed run ---"
run_body="$(
  curl -s -N -X POST -H "$AUTH" -H 'Content-Type: application/json' \
    -d '{"task":"leave the report with the requested content"}' \
    "$BASE/v1/sessions/default/task"
)"
echo "$run_body" | head -40

echo "--- 5. the slot is free again and the conversation is readable ---"
session_code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/default")"
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

echo
echo "==> sessions: two conversations, one run each, at the same time"

# The default conversation is the one the process was started with, and it is in the path like
# every other one - there is no second way to address a conversation.
code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/default/config")"
[ "$code" = "200" ] && ok "the default session is addressable by name" \
  || bad "the default session answered $code, want 200"

# Opening one more conversation is what lets a second front end exist at all.
created="$(curl -s -X POST -H "$AUTH" "$BASE/v1/sessions")"
second="$(printf '%s' "$created" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
if [ -z "$second" ]; then
  bad "a second session could not be opened: $created"
else
  ok "opened a second session"
fi

# Two conversations at once. The first run is launched in the BACKGROUND and the script waits
# until the gateway REPORTS it as running - not for a fixed number of seconds, which would be a
# guess about how fast the machine is. Only then is the second run started, so the overlap is a
# fact rather than a hope.
curl -s -N -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' \
  "$BASE/v1/sessions/$second/task" > .e2e/second-a.sse 2>&1 &
run_a=$!

seen_running=0
for _ in $(seq 1 100); do
  if curl -s -H "$AUTH" "$BASE/v1/sessions" | grep -q "\"id\":\"$second\",\"created\".*\"running\":true"; then
    seen_running=1
    break
  fi
  sleep 0.1
done
if [ "$seen_running" -eq 1 ]; then
  ok "the second session was reported running while it was in flight"
else
  # Saying so is the honest outcome. Passing quietly would be claiming a concurrency check that
  # never actually overlapped anything.
  bad "the run finished too fast to be observed in flight, so the concurrency check could not be made"
fi

# A second run in the SAME conversation, while the first one is STILL in flight. This is checked
# immediately after seeing the run listed, and BEFORE any other run is started, because the plan
# ordered it the other way round and that made it a race: by the time the other run had finished,
# so had this one, the slot was free again, and a 200 was the correct answer to a question that
# was no longer being asked.
code="$(curl -s -o /dev/null -w '%{http_code}' -N -H "$AUTH" \
  -H 'Content-Type: application/json' -d '{"task":"another"}' \
  "$BASE/v1/sessions/$second/task")"
[ "$code" = "409" ] && ok "a second run in the same session was refused with 409" \
  || bad "a second run in the same session answered $code, want 409"

# A run in the DEFAULT session while that one is still in flight: this is a DIFFERENT conversation,
# so it must NOT be refused - a gateway that refused it would be serialising every client against
# every other one, which is the whole thing sessions exist to avoid.
code="$(curl -s -o .e2e/second-b.sse -w '%{http_code}' -N -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' \
  "$BASE/v1/sessions/default/task")"
[ "$code" = "200" ] && ok "a run in a different session was not refused" \
  || bad "a run in a different session answered $code, it must not be refused"

wait "$run_a" || true
if grep -q 'event: done' .e2e/second-a.sse; then
  ok "the run that was in flight ran to completion"
else
  bad "the run that was in flight never finished"
fi

# Closing gives the memory back, and it is gone afterwards.
code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" "$BASE/v1/sessions/$second")"
[ "$code" = "204" ] && ok "closing a session answered 204" \
  || bad "closing a session answered $code, want 204"
code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/$second")"
[ "$code" = "404" ] && ok "a closed session is gone" \
  || bad "a closed session answered $code, want 404"

# The default session belongs to the process that started this gateway: closing it would leave
# that process talking to a conversation that no longer exists.
code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" "$BASE/v1/sessions/default")"
[ "$code" = "409" ] && ok "closing the default session was refused" \
  || bad "closing the default session answered $code, want 409"

echo
[ "$failures" -eq 0 ] || { echo "FAILURES: $failures"; exit 1; }
echo "the gateway works end to end"

