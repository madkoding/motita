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
#                   so the gateway is reached over loopback - always served, whatever the origin
#                   rules are, and the fixture ships none. Publishing a port from 0.0.0.0 would
#                   have meant testing a gateway reached over the host's LAN address instead,
#                   which is a different thing and a different rule.
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
CONTAINER="motita-gateway-e2e-$ARCH"

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

BINARY="dist/.e2e/motita-gateway-linux-$ARCH"
MOCK="dist/.e2e/mockllm-gateway-linux-$ARCH"
case "$BINARY" in dist/.e2e/*) ;; *) echo "ERROR: the test binary must live under dist/.e2e/"; exit 1;; esac
mkdir -p dist/.e2e

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# A container left behind by an interrupted run would otherwise fail this one with docker's
# name-conflict error, which reads as a broken test rather than as stale state. The name is
# ours by construction, so anything wearing it is ours to remove.
cleanup

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
    NO_COLOR=1 MOTITA_LLM_API_KEY=test \
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

# Two conversations at once.
#
# The second request is issued IMMEDIATELY after the first is launched, and the pair is RETRIED
# until the overlap is observed. The earlier version polled /v1/sessions first and only then sent
# the second request, which spent the whole overlap window on polling: a simulated run lasts a
# fraction of a second, so by the time the list answered, the run was over and the slot was free
# - and 200 was the correct answer to a question nobody was asking any more. That is what made
# this flaky rather than wrong, and why "it finished too fast" was reported as a failure.
#
# Retrying is what makes it deterministic instead of a race: a run that ends before the second
# request arrives costs one attempt, not a false failure, and the assertion still has to SEE the
# 409 - a guard that never fires still fails the test.
overlap_seen=0
refused_code=""
# Waits until the gateway stops reporting a run in flight for this session. Needed BETWEEN
# attempts: when the contending request wins the race it starts a run of its own, and the next
# attempt would otherwise meet THAT run's slot and see a 409 that proves nothing.
session_idle() {
  for _ in $(seq 1 100); do
    curl -s -H "$AUTH" "$BASE/v1/sessions" 2>/dev/null | python3 -c '
import json, sys
target = sys.argv[1]
for s in json.load(sys.stdin).get("sessions", []):
    if s["id"] == target and s.get("running"):
        sys.exit(1)
sys.exit(0)
' "$second" && return 0
    sleep 0.1
  done
  return 1
}

for attempt in $(seq 1 25); do
  session_idle || true
  # The run is launched in the background so the client can ask again while it is in flight. Its
  # stream goes to a scratch file and is only kept if this attempt is the one that overlapped,
  # because an attempt whose run was REFUSED writes an error body, not a run.
  curl -s -N -H "$AUTH" -H 'Content-Type: application/json' \
    -d '{"task":"leave the report with the requested content"}' \
    "$BASE/v1/sessions/$second/task" > .e2e/second-a.try 2>&1 &
  run_a=$!

  # Immediately, on the same conversation: this is the request that must be refused while the
  # one above holds the slot. No polling in between - the overlap IS the thing under test.
  refused_code="$(curl -s -o /dev/null -w '%{http_code}' -N -H "$AUTH" \
    -H 'Content-Type: application/json' -d '{"task":"another"}' \
    "$BASE/v1/sessions/$second/task")"

  # The stream is only complete once its curl has returned, so this waits BEFORE reading it: a
  # copy taken earlier holds the preamble and nothing else, which is what "never finished" was
  # reporting about a run that had in fact finished.
  wait "$run_a" 2>/dev/null || true

  if [ "$refused_code" = "409" ]; then
    overlap_seen=1
    cp .e2e/second-a.try .e2e/second-a.sse
    break
  fi
done

if [ "$overlap_seen" -eq 1 ]; then
  ok "a run in flight was observed holding its session's slot (attempt $attempt)"
  ok "a second run in the same session was refused with 409"
else
  bad "a second run in the same session answered $refused_code, want 409 (25 attempts, no overlap ever refused)"
fi

# A run in the DEFAULT session while that one is still in flight: this is a DIFFERENT conversation,
# so it must NOT be refused - a gateway that refused it would be serialising every client against
# every other one, which is the whole thing sessions exist to avoid.
code="$(curl -s -o .e2e/second-b.sse -w '%{http_code}' -N -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' \
  "$BASE/v1/sessions/default/task")"
[ "$code" = "200" ] && ok "a run in a different session was not refused" \
  || bad "a run in a different session answered $code, it must not be refused"

wait "$run_a" 2>/dev/null || true
# The last attempt's run is the one whose stream was kept, and only the attempt that overlapped
# is known to have been in flight when the refusal was observed. `event: done` is asserted on the
# SSE body itself, so a run that was refused rather than started cannot pass this.
if grep -q 'event: done' .e2e/second-a.sse 2>/dev/null; then
  ok "the run that was in flight ran to completion"
else
  bad "the run that was in flight never finished: $(head -3 .e2e/second-a.sse 2>/dev/null)"
fi

# Closing gives the memory back, and it is gone afterwards.
#
# The delete waits for the gateway to REPORT the run as finished rather than assuming it, because
# `event: done` on the stream is not the same instant: the run appends its done event and finishes
# BEFORE the goroutine returns, and the slot is released by a defer that runs after the session is
# saved. So a delete sent the moment the stream closes can still meet a run it considers in flight
# - and 409 is then the correct answer to a question asked too early. The contract is "close it
# after the run finishes", so this waits for exactly that, with a bound.
second_idle=0
for _ in $(seq 1 100); do
  if ! curl -s -H "$AUTH" "$BASE/v1/sessions" | python3 -c '
import json, sys
second = sys.argv[1]
for s in json.load(sys.stdin).get("sessions", []):
    if s["id"] == second and s.get("running"):
        sys.exit(1)   # still running
sys.exit(0)           # idle (or already gone)
' "$second" 2>/dev/null; then
    sleep 0.1
    continue
  fi
  second_idle=1
  break
done
[ "$second_idle" -eq 1 ] && ok "the gateway reported the run finished before the session was closed" \
  || bad "the session never stopped reporting a run in flight"

code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$AUTH" "$BASE/v1/sessions/$second")"
[ "$code" = "204" ] && ok "closing a session answered 204" \
  || bad "closing a session answered $code, want 204"
code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/$second")"
[ "$code" = "404" ] && ok "a closed session is gone" \
  || bad "a closed session answered $code, want 404"

# The default session belongs to the process that started this gateway, so it is not REMOVED -
# but deleting it is a reset rather than a refusal: the transcript is cleared, the title goes back
# to the "New session" placeholder and the session stays. That is what the handler does and what
# the web interface implements ("200 = default session was reset instead of removed"), so 409 was
# the old contract, not the current one. Asserted as the RESET it is: the status, and the state
# left behind.
default_reset="$(curl -s -w '\n%{http_code}' -X DELETE -H "$AUTH" "$BASE/v1/sessions/default")"
default_code="$(printf '%s' "$default_reset" | tail -1)"
default_body="$(printf '%s' "$default_reset" | sed '$d')"
[ "$default_code" = "200" ] && ok "deleting the default session reset it instead of removing it" \
  || bad "deleting the default session answered $default_code, want 200 (a reset)"
case "$default_body" in
  *'"id":"default"'*) ok "the default session still exists after the reset" ;;
  *) bad "the reset answer does not carry the default session: $default_body" ;;
esac
case "$default_body" in
  *'"title":"New session"'*) ok "the reset session got its placeholder title back" ;;
  *) bad "the reset session kept its old title: $default_body" ;;
esac
code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/default")"
[ "$code" = "200" ] && ok "the default session is still addressable after the reset" \
  || bad "the default session answered $code after the reset, want 200"

echo
echo "==> a client that disconnects and comes back"

# The client starts a turn and is then KILLED, which is what a locked phone screen looks like from
# the gateway's side: the socket goes away without a goodbye.
#
# The moment of the kill is not a guess about how fast the machine is. The script waits until the
# gateway itself REPORTS the run in flight AND having said something, and only then cuts the client
# off - so the turn really is in the middle when its client disappears. Killing it against a fixed
# timeout was tried first and proved nothing: the turn had already finished by the time the next
# request arrived, and the whole block passed while testing nothing.
curl -s -N -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' \
  "$BASE/v1/sessions/default/task" > .e2e/first.sse 2>&1 &
first_pid=$!

seen_running=0
for _ in $(seq 1 400); do
  if curl -s -H "$AUTH" "$BASE/v1/sessions/default/run" -o .e2e/run.json 2>/dev/null \
     && grep -q '"last_seq":[1-9]' .e2e/run.json; then
    seen_running=1
    break
  fi
  sleep 0.05
done
kill "$first_pid" 2>/dev/null || true
wait "$first_pid" 2>/dev/null || true

SEEN="$(grep -o '^id: [0-9]*' .e2e/first.sse 2>/dev/null | tail -1 | grep -o '[0-9]*' || true)"
echo "    the client left after event ${SEEN:-none}"
[ "$seen_running" -eq 1 ] \
  && ok "the run was in flight, and had spoken, when its client was killed" \
  || bad "the run was never observed in flight with events, so the disconnect tested nothing"

# Asking about the run WHILE it is still in flight, and then attaching from where the first client
# stopped, is the pair that proves resumption rather than merely proving the turn finished.
status_code="$(curl -s -o .e2e/run.json -w '%{http_code}' -H "$AUTH" \
  "$BASE/v1/sessions/default/run")"
case "$status_code" in
  200) ok "the gateway reported the run in flight after its client left" ;;
  # 404 is honest here: the turn ended before the question arrived. Saying so is the point, and the
  # checks below still hold - they just prove less about resuming, which the output says.
  404) ok "the run had already finished when asked, so the resume path was not observed in flight" ;;
  *)   bad "asking about the run answered $status_code" ;;
esac

# The second client attaches FROM WHERE THE FIRST ONE STOPPED. Asking from 0 instead would replay
# the beginning, which is the duplication the sequence numbers exist to avoid.
curl -s -N -o .e2e/resumed.sse -H "$AUTH" \
  "$BASE/v1/sessions/default/events?from=${SEEN:-0}" 2>&1 || true

# The preamble is always the first thing a reattaching client receives, and it says what the log can
# still give it - so a client can tell "resumed" from "I lost lines".
grep -q 'event: attached' .e2e/resumed.sse \
  && ok "a reattaching client is greeted with a preamble" \
  || bad "a reattaching client got no preamble: $(head -3 .e2e/resumed.sse 2>/dev/null)"

# The preamble carries the sequence range and the eviction count, which is what turns "there is a
# hole" into "there is a hole of this size".
grep -q '"last_seq"' .e2e/resumed.sse \
  && ok "the preamble reports where the log is" \
  || bad "the preamble does not report the log's range"

# And it must NOT replay what the first client already saw: that is the duplication the numbering
# exists to prevent, and a client would render the same line twice.
if [ -n "$SEEN" ] && grep -q "^id: ${SEEN}$" .e2e/resumed.sse; then
  bad "resuming from $SEEN replayed that event, so a client would render it twice"
else
  ok "resuming from ${SEEN:-0} did not replay the event the first client had"
fi

# Every event carries its number, which is what the client sends back to resume.
grep -q '^id: 0$' .e2e/resumed.sse \
  && ok "the reattached stream carries sequence numbers" \
  || bad "the reattached stream carries no sequence numbers"

# Attaching with nothing running says so, rather than hanging on a stream that will never produce.
deadline=$((SECONDS + 20))
while [ "$SECONDS" -lt "$deadline" ]; do
  if curl -s -H "$AUTH" "$BASE/v1/sessions/default/run" -o /dev/null -w '%{http_code}' | grep -q 404; then
    break
  fi
  sleep 0.2
done
code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions/default/events?from=0")"
[ "$code" = "404" ] && ok "attaching with no run in flight answered 404" \
  || bad "attaching with no run in flight answered $code, want 404"

# The turn finished and left its work on disk even though the client that started it went away. This
# is the consequence that matters: the turn was not thrown away with the connection.
[ -f .e2e/work/report.txt ] \
  && ok "the turn outlived the client that started it and left its file behind" \
  || bad "the turn was thrown away when its client disconnected"
if [ -f .e2e/work/report.txt ]; then
  resumed_content="$(cat .e2e/work/report.txt)"
  [ "$resumed_content" = "content-valid" ] \
    && ok "the resumed turn wrote the requested contents" \
    || bad "the resumed turn wrote '$resumed_content'"
fi

# The API key must not cross this wire either. This scans the bodies collected in this block, which
# makes it a check of the RESPONSES rather than of the code that writes them.
if grep -q 'sk-' .e2e/resumed.sse .e2e/run.json 2>/dev/null; then
  bad "an API key appeared in a response"
else
  ok "no API key in the resumed responses"
fi

echo


echo
echo "==> the conversation a client comes back to"

# This is the check that a unit test could not make. The gateway's own tests for this endpoint use a
# fake service with a hand-written transcript, so they agreed with their own fake while the REAL
# agent recorded nothing: a task ran, and the conversation read back EMPTY - the blank screen that
# coming back to a conversation exists to avoid. Driving the running gateway is what closes that gap.
transcript="$(curl -s -H "$AUTH" "$BASE/v1/sessions/default/messages")"
case "$transcript" in
  *'"messages":[]'*)
    bad "the conversation is EMPTY after a task ran, so a client coming back reads a blank screen"
    ;;
  *'leave the report'*)
    ok "the conversation carries the task that ran"
    ;;
  *)
    bad "the conversation does not carry the task: $transcript"
    ;;
esac

# And it is recorded as WORK THAT RAN, not as a chat reply: an interface draws those differently,
# and a task shown as a reply would tell the user nothing was done.
case "$transcript" in
  *'"Kind":"task"'*) ok "the turn is recorded as a task, not as a reply" ;;
  *) bad "the turn is not recorded as a task: $transcript" ;;
esac

# The outcome is in it too. A turn with the request and no answer would read as a question that was
# never dealt with.
case "$transcript" in
  *'"Agent":""'*) bad "the turn carries the request but no outcome" ;;
  *) ok "the turn carries what was done about it" ;;
esac

echo
[ "$failures" -eq 0 ] || { echo "FAILURES: $failures"; exit 1; }
echo "the gateway works end to end"

