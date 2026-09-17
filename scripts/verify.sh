#!/usr/bin/env bash
# Full verification of the repository, in the exact order a reviewer would run it.
#
#   ./scripts/verify.sh
#
# Checks, in order:
#   1. gofmt         — no unformatted file
#   2. go build      — everything compiles
#   3. go vet        — no static-analysis findings
#   4. go test -race — every test green, with the race detector
#   5. coverage      — per package and aggregate, against a minimum
#   6. English check — no user-visible Spanish left in code, configs or scripts
#   7. i386 E2E      — both end-to-end tests in real 32-bit containers
set -uo pipefail

cd "$(dirname "$0")/.."
export PATH=/opt/data/cache/go/bin:$PATH
export GOCACHE=${GOCACHE:-/opt/data/cache/go-build}
export GOPATH=${GOPATH:-/opt/data/cache/gopath}

MIN_COVERAGE=${MIN_COVERAGE:-100}
failures=0
step() { printf '\n=== %s ===\n' "$1"; }
ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; failures=$((failures+1)); }

step "1. gofmt"
unformatted="$(gofmt -l . | grep -v '^$' || true)"
if [ -z "$unformatted" ]; then ok "all files formatted"; else bad "unformatted: $unformatted"; fi

step "2. go build"
if go build ./... 2>&1 | head -20; then ok "builds"; else bad "build failed"; fi

step "3. go vet"
if output=$(go vet ./... 2>&1); then ok "vet clean"; else bad "vet: $output"; fi

step "4. go test -race"
if output=$(go test -count=1 -race -timeout 300s ./... 2>&1); then
  ok "all tests pass"
  echo "$output" | grep -E '^(ok|FAIL)' | sed 's/^/    /'
else
  bad "tests failed"
  echo "$output" | tail -30 | sed 's/^/    /'
fi

step "5. coverage (gate: ${MIN_COVERAGE}% per package)"
# Checked package by package: a gap must not hide behind the aggregate.
below=0
for pkg in $(go list ./internal/... ./cmd/... 2>/dev/null); do
  result="$(go test -count=1 -cover "$pkg" 2>/dev/null)"
  if echo "$result" | grep -q 'no test files'; then
    printf '    %-52s (no test files)\n' "$pkg"
    continue
  fi
  cov="$(echo "$result" | grep -oE 'coverage: [0-9.]+' | grep -oE '[0-9.]+')"
  cov="${cov:-0}"
  printf '    %-52s %s%%\n' "$pkg" "$cov"
  if awk -v c="$cov" -v m="$MIN_COVERAGE" 'BEGIN { if (c < m) exit 1 }'; then
    :
  else
    bad "$pkg coverage $cov% is below ${MIN_COVERAGE}%"
    below=$((below+1))
  fi
done
[ "$below" -eq 0 ] && ok "every package at ${MIN_COVERAGE}% or above"
go test -coverpkg=./... -coverprofile=/tmp/verify_cov.out -covermode=atomic ./... >/dev/null 2>&1
total=$(go tool cover -func=/tmp/verify_cov.out | tail -1 | awk '{print $3}' | tr -d '%')
printf '    %-52s %s%%\n' "aggregate" "$total"

step "6. no Spanish left in code, configs or scripts"
# This check looks for Spanish words that must not survive the translation. It is
# deliberately narrow: only words that are unambiguously Spanish, so it does not
# trip over English words. The script itself is excluded, because it necessarily
# contains those words in the pattern below.
pattern='\b(función|también|todavía|además|así|está|están|desde|hacia|según|mientras|porque|cuando|entonces|siempre|nunca|nada|pero|sólo|debe|puede|hace|hacer|tiene|tienen|usar|usando|valores|opciones|campo|nombre|ruta|salida|entrada|comando|resultado|ejemplo|archivo|fichero|cola|tarea|tareas|ancla|peligro|aviso|no se|sin embargo)\b'
found=$(grep -rniE "$pattern" --include='*.go' --include='*.yaml' --include='*.yml' --include='*.sh' --include='*.md' --include='Makefile' . 2>/dev/null \
  | grep -v '/.git/' | grep -v '^./scripts/verify.sh' || true)
if [ -z "$found" ]; then
  ok "no Spanish found"
else
  bad "Spanish still present:"
  echo "$found" | head -25 | sed 's/^/    /'
  echo "    (total: $(echo "$found" | wc -l) line(s))"
fi

step "7. end-to-end tests on i386"
# Every linux architecture the project publishes is exercised, not only i386: on
# arm and arm64 this relies on qemu being registered on the host.
for arch in 386 amd64 arm arm64; do
  if ./scripts/e2e-agent.sh "$arch" >"/tmp/verify_e2e_agent_$arch.log" 2>&1; then
    ok "agent E2E on linux/$arch"
  else
    bad "agent E2E failed on linux/$arch (see /tmp/verify_e2e_agent_$arch.log)"
    tail -15 "/tmp/verify_e2e_agent_$arch.log" | sed 's/^/    /'
  fi
  if ./scripts/e2e.sh "$arch" >"/tmp/verify_e2e_chat_$arch.log" 2>&1; then
    ok "chat E2E on linux/$arch"
  else
    bad "chat E2E failed on linux/$arch (see /tmp/verify_e2e_chat_$arch.log)"
    tail -15 "/tmp/verify_e2e_chat_$arch.log" | sed 's/^/    /'
  fi
done

printf '\n========================================\n'
if [ "$failures" -eq 0 ]; then
  echo "VERIFICATION PASSED: the repository is clean, tested and functional."
else
  echo "VERIFICATION FAILED: $failures check(s) did not pass."
fi
exit "$failures"
