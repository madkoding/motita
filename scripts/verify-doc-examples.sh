#!/bin/sh
# Run the code examples of the Documentation pages against a real gateway.
#
# The examples live in scripts/docs-examples.mjs, one per language, and are printed on
# the site. An example that no longer runs - because the wire format moved, or a client
# library renamed a call - is worse than none: the reader trusts it. So each one is
# executed here, against the real gateway and the simulated LLM (tools/mockllm), and
# must finish cleanly with output.
#
#   WebSocket client   JavaScript, Python, Go, Rust   (TypeScript is type-checked)
#   Server-sent events JavaScript, Python, Go, Rust   (TypeScript is type-checked)
#   Create a schedule  curl, JavaScript, Python, Go, Rust (TypeScript is type-checked)
#
# Needs: go, node and npm, python3 and pip, cargo, curl. It uses the fixed ports 7477
# (gateway) and 8210 (simulated LLM), because those are the ones the examples print.
#
# Usage: scripts/verify-doc-examples.sh
# Exit codes: 0 every example ran · 1 one did not.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
failed=0
pids=""

cleanup() {
  for pid in $pids; do
    kill "$pid" 2>/dev/null || true
  done
  rm -rf "$work"
}
trap cleanup EXIT

for port in 7477 8210; do
  if curl -s -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null; then
    printf 'error: port %s is already in use; stop what is listening there first\n' "$port" >&2
    exit 1
  fi
done

printf '==> building the gateway and the simulated LLM\n'
(cd "$root" && go build -o "$work/motita" ./cmd/agent && go build -o "$work/mockllm" ./tools/mockllm)

mkdir -p "$work/home" "$work/run"
cp "$root/configs/e2e-task.txt" "$work/task.txt"
sed "s#/e2e#$work#g; s#127.0.0.1:8321#127.0.0.1:7477#" "$root/configs/e2e-agent.yaml" > "$work/config.yaml"

"$work/mockllm" -port 8210 -delay-ms 20 > "$work/mock.log" 2>&1 &
pids="$pids $!"
(cd "$work" && HOME="$work/home" NO_COLOR=1 MOTITA_LLM_API_KEY=test \
  "$work/motita" -config "$work/config.yaml" -serve -gateway 127.0.0.1:7477 > "$work/gateway.log" 2>&1) &
pids="$pids $!"

i=0
until [ -s "$work/gateway.token" ] && curl -fsS -o /dev/null http://127.0.0.1:7477/v1/health 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 150 ]; then
    printf 'error: the gateway never came up:\n' >&2
    cat "$work/gateway.log" >&2
    exit 1
  fi
  sleep 0.2
done
MOTITA_TOKEN="$(cat "$work/gateway.token")"
export MOTITA_TOKEN

# Write every example to a file named <topic>.<ext>.
node --input-type=module -e '
import { scheduleExamples, sseExamples, wsExamples } from "'"$root"'/scripts/docs-examples.mjs";
import { writeFileSync } from "node:fs";
const ext = { js: "mjs", ts: "ts", py: "py", go: "go", rust: "rs", sh: "sh" };
const topics = { ws: wsExamples, sse: sseExamples, schedule: scheduleExamples };
for (const [topic, examples] of Object.entries(topics)) {
  for (const [lang, source] of examples("en")) {
    writeFileSync(process.argv[1] + "/" + topic + "." + ext[lang], source + "\n");
  }
}
' "$work/run"

# check <label> <command...>: runs it and wants a clean exit with some output.
check() {
  label="$1"
  shift
  if out="$("$@" 2>&1)" && [ -n "$out" ]; then
    printf '  ok   %s\n' "$label"
  else
    printf '  FAIL %s\n%s\n' "$label" "$out" >&2
    failed=1
  fi
}

cd "$work/run"

printf '==> installing the client libraries\n'
python3 -m venv venv
./venv/bin/pip install -q websocket-client requests
npm init -y > /dev/null
npm install --silent ws @types/ws typescript @types/node
for topic in ws sse schedule; do
  mkdir -p "go-$topic" "rust-$topic/src"
  cp "$topic.go" "go-$topic/main.go"
  cp "$topic.rs" "rust-$topic/src/main.rs"
done
(cd go-ws && go mod init example.com/ws > /dev/null 2>&1 && go get github.com/gorilla/websocket github.com/google/uuid > /dev/null 2>&1)
(cd go-sse && go mod init example.com/sse > /dev/null 2>&1)
(cd go-schedule && go mod init example.com/schedule > /dev/null 2>&1)
cat > rust-ws/Cargo.toml <<'EOF'
[package]
name = "ws"
version = "0.1.0"
edition = "2021"
[dependencies]
tungstenite = "0.26"
serde_json = "1"
uuid = { version = "1", features = ["v4"] }
chrono = "0.4"
EOF
for topic in sse schedule; do
  cat > "rust-$topic/Cargo.toml" <<EOF
[package]
name = "$topic"
version = "0.1.0"
edition = "2021"
[dependencies]
reqwest = { version = "0.12", features = ["blocking", "json"] }
serde_json = "1"
EOF
done

printf '==> running the examples\n'
check "ws · JavaScript" node ws.mjs
check "ws · Python" ./venv/bin/python ws.py
check "ws · Go" sh -c 'cd go-ws && go run .'
check "ws · Rust" sh -c 'cd rust-ws && cargo run -q'
check "sse · JavaScript" node sse.mjs
check "sse · Python" ./venv/bin/python sse.py
check "sse · Go" sh -c 'cd go-sse && go run .'
check "sse · Rust" sh -c 'cd rust-sse && cargo run -q'
check "schedule · curl" sh schedule.sh
check "schedule · JavaScript" node schedule.mjs
check "schedule · Python" ./venv/bin/python schedule.py
check "schedule · Go" sh -c 'cd go-schedule && go run .'
check "schedule · Rust" sh -c 'cd rust-schedule && cargo run -q'
# The TypeScript ones are type-checked, not run: that is where their value is.
check "TypeScript · strict type check" sh -c \
  'npx tsc --noEmit --strict --target es2022 --module esnext --moduleResolution bundler --skipLibCheck ws.ts sse.ts schedule.ts && echo typed'

if [ "$failed" -ne 0 ]; then
  printf 'FAILED: an example on the site no longer runs\n' >&2
  exit 1
fi
printf 'every documentation example runs\n'
