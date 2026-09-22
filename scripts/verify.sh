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
#
# tools/ is exempt, and the exemption is STATED rather than left to the accident of a
# package having no test files. These are test harnesses and development servers: what is
# uncovered in them is their own assertion-failure and skip branches, which only run when a
# test has already failed. Holding a harness to the same bar as the program would mean
# writing tests for the tests, and the branches that would be covered are the ones that fire
# on failure — so the coverage number would go up without a single new check.
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
for pkg in $(go list ./tools/... 2>/dev/null); do
  result="$(go test -count=1 -cover "$pkg" 2>/dev/null)"
  cov="$(echo "$result" | grep -oE 'coverage: [0-9.]+' | grep -oE '[0-9.]+')"
  if echo "$result" | grep -q 'no test files'; then
    printf '    %-52s (harness, no tests of its own)\n' "$pkg"
  else
    printf '    %-52s %s%% (harness)\n' "$pkg" "${cov:-0}"
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
#
# Only GIT-TRACKED files are searched. An earlier version walked the whole tree and
# therefore flagged the generated .starlight/README.md — a file written by a local install,
# listed in .gitignore, and never committed. A check that fails on something the repo does
# not carry is a check that fails on every machine for a different reason, and the real
# finding it was written for hides among the noise. Searching what is tracked also makes
# the result the same everywhere, which is what a gate has to be.
#
# Test files are excluded, and that exclusion is the point of the check rather than a way
# around it. Several suites feed Spanish IN on purpose: ask_test.go covers the case where a
# model answers with a Spanish conditional, policy_test.go uses "salida" as a filename. Those
# fixtures are the feature being tested, not a translation that was missed, and a gate that
# cannot tell the difference between them has to be narrowed until it can.
#
# A production comment may also QUOTE the Spanish it handles — the leading conditional a model
# emits, which cleanAssumption exists to strip. That is a citation of real input, not shipped
# copy, so a line carrying the marker below is exempt. The marker is required: it makes the
# exemption a deliberate statement by the author rather than something the gate guessed at.
# Two independent detectors, because one word list was not enough.
#
# A hand-kept list of Spanish words was the original approach, and a full audit found twelve
# production lines carrying Spanish that it had never flagged: strings like "¿Puedes decirme,
# en una frase, qué quieres que haga?" contain no word from any list of common words, because
# an interrogative sentence is built from verbs and pronouns the list did not have. A list can
# only ever contain what somebody remembered to add.
#
# The first detector is CHARACTER-based and needs no list: Spanish is the only reason these
# characters appear at all (á é í ó ú ñ ¿ ¡ ü). It is what a stale translation actually leaves
# behind, and it cannot be forgotten.
#
# The second is the word list, kept for what the characters miss: unaccented Spanish words
# (salida, tarea, comando) look like ordinary English text to a character test.
pattern='\b(función|también|todavía|además|así|está|están|desde|hacia|según|mientras|porque|cuando|entonces|siempre|nunca|nada|pero|sólo|debe|puede|hace|hacer|tiene|tienen|usar|usando|valores|opciones|campo|nombre|ruta|salida|entrada|comando|resultado|ejemplo|archivo|fichero|cola|tarea|tareas|ancla|peligro|aviso|no se|sin embargo)\b'
accents='[áéíóúüñÁÉÍÓÚÜÑ¿¡]'
found=$(
  {
    git ls-files -z 2>/dev/null \
      | grep -zE '\.(go|ya?ml|sh|md)$|(^|/)Makefile$' \
      | grep -zv '_test\.go$' \
      | xargs -0 -r grep -lE "$accents" 2>/dev/null
    git ls-files -z 2>/dev/null \
      | grep -zE '\.(go|ya?ml|sh|md)$|(^|/)Makefile$' \
      | grep -zv '_test\.go$' \
      | xargs -0 -r grep -liE "$pattern" 2>/dev/null
  } | sort -u | grep -v '^scripts/verify\.sh$' | xargs -r grep -niE "$accents|$pattern" 2>/dev/null \
  | grep -v 'spanish-fixture:' || true
)
if [ -z "$found" ]; then
  ok "no Spanish found"
else
  bad "Spanish still present:"
  echo "$found" | head -25 | sed 's/^/    /'
  echo "    (total: $(echo "$found" | wc -l) line(s))"
fi

step "7. the installer script is valid POSIX sh"
# It is piped to `sh` on systems whose /bin/sh is dash or busybox ash, so a bash
# construct would break it exactly where nobody can debug it. Parsed here, not
# run: running it would download a release, which a verification run must not do.
if dash -n scripts/install.sh 2>/dev/null; then
  ok "parses as POSIX sh (dash)"
elif command -v dash >/dev/null 2>&1; then
  bad "scripts/install.sh is not valid dash syntax"
else
  sh -n scripts/install.sh && ok "parses as sh" || bad "scripts/install.sh does not parse"
fi

step "7b. the e2e scripts cannot overwrite a released artifact"
# This is a regression guard, not a style check. Both e2e scripts used to build
# their test binary straight into dist/starlight-linux-<arch>, which is the path
# the CI uploads as the release asset — so every published binary was the test
# build, stamped version "e2e" instead of the tag. Nothing in the published
# names may be written by a test.
leak=0
for script in scripts/e2e.sh scripts/e2e-agent.sh; do
  # The guard checks the DESTINATION, not the spelling of the variable holding it.
  #
  # It used to flag any `-o "$SOMETHING"`, which failed both scripts on every run even
  # though both build under dist/.e2e/ and e2e-agent.sh even asserts it. A gate that cries
  # wolf is worse than no gate: it trains the reader to skip the line, and then it cannot
  # warn about the regression it was written for. What matters is where the variable
  # actually points, so that is what is resolved here.
  #
  # The variables that hold the safe directory, so a destination may be built from one of
  # them: e2e.sh spells it E2E_DIST and composes TEST_BINARY from it.
  safe="$(grep -oE '^[[:space:]]*[A-Z_][A-Z0-9_]*="?dist/\.e2e' "$script" | grep -oE '^[[:space:]]*[A-Z_][A-Z0-9_]*' | tr -d '[:space:]' | sort -u)"
  dests="$(grep -oE '\-o "\$[A-Z_][A-Z0-9_]*"' "$script" | grep -oE '\$[A-Z_][A-Z0-9_]*' | tr -d '$' | sort -u)"
  for v in $dests; do
    asg="$(grep -E "^[[:space:]]*$v=" "$script" | head -1)"
    under=0
    case "$asg" in *dist/.e2e*) under=1 ;; esac
    for s in $safe; do
      case "$asg" in *"\$$s"*|*"\${$s}"*) under=1 ;; esac
    done
    if [ "$under" -eq 0 ]; then
      bad "$script: -o \"\$$v\" is not assigned under dist/.e2e/ ($asg)"
      leak=$((leak+1))
    fi
  done
  # A literal destination may never name a published artifact.
  if grep -qE '\-o "?dist/(starlight|mock)[^"]*"' "$script"; then
    bad "$script builds directly into a published path"
    leak=$((leak+1))
  fi
done
[ "$leak" -eq 0 ] && ok "both e2e scripts build only under dist/.e2e/"

step "8. end-to-end tests on linux"
# Every linux architecture the project publishes is exercised.
for arch in 386 amd64 arm arm64; do
  if ./scripts/e2e-agent.sh "$arch" >"/tmp/verify_e2e_agent_$arch.log" 2>&1; then
    ok "agent E2E on linux/$arch"
  else
    bad "agent E2E failed on linux/$arch (see /tmp/verify_e2e_agent_$arch.log)"
    tail -15 "/tmp/verify_e2e_agent_$arch.log" | sed 's/^/    /'
  fi
  if ./scripts/e2e.sh "$arch" >"/tmp/verify_e2e_$arch.log" 2>&1; then
    ok "E2E on linux/$arch"
  else
    bad "E2E failed on linux/$arch (see /tmp/verify_e2e_$arch.log)"
    tail -15 "/tmp/verify_e2e_$arch.log" | sed 's/^/    /'
  fi
done

printf '\n========================================\n'
if [ "$failures" -eq 0 ]; then
  echo "VERIFICATION PASSED: the repository is clean, tested and functional."
else
  echo "VERIFICATION FAILED: $failures check(s) did not pass."
fi
exit "$failures"
