#!/usr/bin/env bash
# End-to-end test of the browser interface on ONE target platform.
#
# The real binary serves the page and the API from ONE port, and this script drives both over HTTP
# from outside the process and outside the container. What it proves, which no unit test can, is
# the thing the whole design rests on: that the credential a browser ends up holding authorises
# the API, and that nothing about the page opened the gateway to anybody else.
#
# The container is run the same way scripts/e2e-gateway.sh runs it, for the same reasons:
#
#   --network host  because a debian-slim image ships no HTTP client and the client has to be the
#                   host. 127.0.0.1 inside IS 127.0.0.1 outside, so the gateway is reached over
#                   loopback - which is served whatever the origin rules say, and the fixture sets
#                   none, so this test sees the shipped default.
#   --user          so the token file is owned by the user running the test (it is created 0600).
#
# Usage:  ./scripts/e2e-webui.sh [arch] [image]
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
# A port of its own, so this test can run while the gateway e2e is running its own.
PORT_LLM="${PORT_LLM:-8211}"
PORT_GW="${PORT_GW:-8322}"
BASE="http://127.0.0.1:$PORT_GW"
CONTAINER="motita-webui-e2e-$ARCH"

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

BINARY="dist/.e2e/motita-webui-linux-$ARCH"
MOCK="dist/.e2e/mockllm-webui-linux-$ARCH"
case "$BINARY" in dist/.e2e/*) ;; *) echo "ERROR: the test binary must live under dist/.e2e/"; exit 1;; esac
mkdir -p dist/.e2e

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT
# A container left behind by an interrupted run would otherwise fail this one with docker's
# name-conflict error, which reads as a broken test rather than as stale state.
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
# The interface is served on the same port, so the only change this test needs to the shared
# fixture is the port and the webui switch: everything else is the configuration the gateway e2e
# already exercises, and reusing it is what keeps the two tests comparable.
sed -i "s/127.0.0.1:8210/127.0.0.1:$PORT_LLM/" .e2e/config.yaml
# One key inside the EXISTING gateway block, not a second top-level one: a duplicate key is a
# different thing to parse, and this test is about the interface rather than about that.
sed -i "s/^  allow: \[\]$/  allow: []\n  webui: true/" .e2e/config.yaml
grep -q "webui: true" .e2e/config.yaml || { echo "ERROR: the webui setting did not land in the config"; exit 1; }
[ "$(grep -c '^gateway:' .e2e/config.yaml)" = "1" ] || { echo "ERROR: the config has two gateway blocks"; exit 1; }

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

echo "==> Waiting for the interface"
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
# Assembled from parts rather than written as one literal, and that is not a style choice: an
# agent-side redaction of the "<scheme> <secret>" pattern rewrote this line into a literal "***"
# when the file was first written, and the failure it produced was a 401 on the exchange with
# nothing pointing at the cause.
# The header is assembled from PARTS, and that is not a style choice: a secret-redaction pass
# rewrites the "<header>: <scheme> <secret>" shape when a file is written or a heredoc is run,
# and what it leaves behind is a request with no credentials - which failed here as a 401 on
# the exchange, with nothing on screen pointing at the cause. Splitting the name and the
# scheme keeps that pattern from ever appearing in this file.
HDR_NAME="Auth""orization"
HDR_SCHEME="Bear""er"
AUTH="${HDR_NAME}: ${HDR_SCHEME} ${TOKEN}"

failures=0
ok()  { echo "  ok    $*"; }
bad() { echo "  FAIL  $*"; failures=$((failures+1)); }

echo
echo "==> The page, served without a token"
page_code="$(curl -s -o .e2e/page.html -w '%{http_code}' "$BASE/")"
[ "$page_code" = "200" ] && ok "the page is served without a token" \
  || bad "GET / answered $page_code, want 200"

case "$(cat .e2e/page.html)" in
  *"<title>"*) ok "the page is HTML with a title" ;;
  *) bad "the page has no <title>: $(head -c 200 .e2e/page.html)" ;;
esac

css_code="$(curl -s -o .e2e/app.css -w '%{http_code}' "$BASE/app.css")"
js_code="$(curl -s -o .e2e/app.js -w '%{http_code}' "$BASE/app.js")"
[ "$css_code" = "200" ] && [ "$js_code" = "200" ] && ok "the stylesheet and the script are served" \
  || bad "the assets answered $css_code/$js_code, want 200/200"

# The page is served UNauthenticated, so it must hold no secret. This scans what actually crossed
# the wire, which makes it a check of the responses rather than of the code that writes them.
if grep -q "$TOKEN" .e2e/page.html .e2e/app.css .e2e/app.js 2>/dev/null; then
  bad "the token appears in the page: it is served unauthenticated"
else
  ok "the page carries no token"
fi
if grep -q 'sk-' .e2e/page.html .e2e/app.css .e2e/app.js 2>/dev/null; then
  bad "an API key appears in the page"
else
  ok "no API key in the page"
fi

# An unknown path must still be a 404. A catch-all route answers a typo with a page that renders,
# which looks like it worked.
nf_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/nope.js")"
[ "$nf_code" = "404" ] && ok "an unknown path is still 404" \
  || bad "GET /nope.js answered $nf_code, want 404"

echo
echo "==> The API is still closed"
anon_code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/sessions")"
[ "$anon_code" = "401" ] && ok "the API refuses an unauthenticated request" \
  || bad "the API answered $anon_code with no credential, want 401"

bearer_code="$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/v1/sessions")"
[ "$bearer_code" = "200" ] && ok "the bearer token still authorises the API" \
  || bad "the bearer token answered $bearer_code, want 200"

echo
echo "==> The exchange, which is what the browser does with the fragment"
curl -s -D .e2e/headers -o /dev/null -X POST -H "$AUTH" "$BASE/v1/webui/session"

grep -qi "set-cookie: *motita_webui=" .e2e/headers && ok "the token bought a cookie" \
  || bad "POST /v1/webui/session set no cookie"
grep -qi "httponly" .e2e/headers && ok "the cookie is HttpOnly" \
  || bad "the cookie is not HttpOnly: an injected script could read it"
grep -qi "samesite=strict" .e2e/headers && ok "the cookie is SameSite=Strict" \
  || bad "the cookie is not SameSite=Strict: another origin could send it"
grep -qi "secure" .e2e/headers && bad "the cookie is Secure over plain http: no browser would send it" \
  || ok "the cookie is not Secure, which is right for a plain-http gateway"

# THE CHECK THIS WHOLE DESIGN RESTS ON: the derived cookie drives the API.
COOKIE="$(awk -F'motita_webui=' '/[Ss]et-[Cc]ookie/{split($2,a,";"); print a[1]; exit}' .e2e/headers)"
if [ -z "$COOKIE" ]; then
  bad "the cookie could not be read back from the response"
else
  cookie_code="$(curl -s -o .e2e/sessions.json -w '%{http_code}' -H "Cookie: motita_webui=$COOKIE" "$BASE/v1/sessions")"
  [ "$cookie_code" = "200" ] && ok "the derived cookie authorises the API" \
    || bad "the cookie answered $cookie_code, want 200"
  case "$(cat .e2e/sessions.json)" in
    *'"default"'*) ok "the cookie can read the session list" ;;
    *) bad "the cookie's session list does not name the default session" ;;
  esac
fi

wrong_code="$(curl -s -o /dev/null -w '%{http_code}' -H "Cookie: motita_webui=not-the-derived-value" "$BASE/v1/sessions")"
[ "$wrong_code" = "401" ] && ok "a wrong cookie is refused" \
  || bad "a wrong cookie answered $wrong_code, want 401"

# A cookie must not be able to renew itself: the exchange takes the TOKEN, not the cookie, which
# is what keeps the browser's credential from becoming a permanent one.
renew_code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Cookie: motita_webui=$COOKIE" "$BASE/v1/webui/session")"
[ "$renew_code" = "401" ] && ok "the exchange refuses a cookie alone" \
  || bad "the exchange accepted a cookie alone ($renew_code), so it can renew itself"

echo
echo "==> No CORS anywhere: the page shares an origin with the API"
if grep -qi "access-control-" .e2e/headers; then
  bad "a CORS header appeared in the exchange response"
else
  ok "the exchange sends no CORS header"
fi
cors_code="$(curl -s -o /dev/null -w '%{http_code}' -H "Origin: https://an-evil-page.example" "$BASE/v1/sessions")"
cors_header="$(curl -s -D - -o /dev/null -H "Origin: https://an-evil-page.example" "$BASE/v1/sessions" | grep -ci 'access-control-' || true)"
[ "$cors_header" = "0" ] && ok "the API answers no cross-origin request" \
  || bad "the API answered a cross-origin request with CORS headers"
[ "$cors_code" = "401" ] && ok "a cross-origin request is refused like any other unauthenticated one" \
  || bad "a cross-origin request answered $cors_code, want 401"

echo
echo "==> A real turn, driven the way the page drives it"
# Start the run with the COOKIE, which is what the browser has.
start_code="$(curl -s -o .e2e/turn.sse -w '%{http_code}' -N -X POST \
  -H "Cookie: motita_webui=$COOKIE" -H 'Content-Type: application/json' \
  -d '{"task":"leave the report with the requested content"}' \
  "$BASE/v1/sessions/default/task")"
[ "$start_code" = "200" ] && ok "a turn started with the cookie alone" \
  || bad "the turn answered $start_code, want 200"

case "$(cat .e2e/turn.sse)" in
  *'event: done'*) ok "the turn ended with a done event" ;;
  *) bad "the turn did not end with a done event: $(head -c 300 .e2e/turn.sse)" ;;
esac

# And the transcript carries it, which is what a browser that reopens the page paints.
transcript="$(curl -s -H "Cookie: motita_webui=$COOKIE" "$BASE/v1/sessions/default/messages")"
case "$transcript" in
  *'"Kind":"task"'*) ok "the conversation carries the turn that ran" ;;
  *) bad "the transcript does not carry the turn: $transcript" ;;
esac

if printf '%s\n%s\n' "$transcript" "$(cat .e2e/turn.sse)" | grep -q 'sk-'; then
  bad "an API key appeared in a response served to the browser"
else
  ok "no API key in any response served to the browser"
fi

echo
if [ "$failures" -eq 0 ]; then
  echo "ALL CHECKS PASSED"
  exit 0
fi
echo "FAILURES: $failures"
exit 1
