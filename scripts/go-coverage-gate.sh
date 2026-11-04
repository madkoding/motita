#!/bin/sh
# The Go coverage gate: every package with tests must be at 100 % of its statements, checked
# package by package so a gap cannot hide behind the aggregate.
#
# It reads coverage PROFILES, not `go test` output, because CI runs the suite in parts on
# separate runners (internal/gateway's tests are split in two): no single log line carries a
# split package's figure, but the profiles add up. The profiles are made without -coverpkg, so
# each package is measured by its own tests only, exactly what `go test -cover <pkg>` prints,
# and the figure is rounded the way go test prints it (one decimal).
#
# Usage: scripts/go-coverage-gate.sh MERGED_OUT PROFILE...
#   writes the merged profile to MERGED_OUT (Sonar reads it) and gates every package.
# Exit codes: 0 every package passes · 1 one is below the gate · 2 bad usage.
set -eu

if [ "$#" -lt 2 ]; then
  echo "usage: $0 MERGED_OUT PROFILE..." >&2
  exit 2
fi
merged="$1"
shift

# One mode line, then each block once with its hit counts summed: a block the two halves of a
# package both ran is covered, one neither ran stays at 0.
awk '
  FNR == 1 { if (mode == "") mode = $0; next }
  { k = $1 " " $2; if (!(k in n)) order[++m] = k; n[k] += $3 }
  END { print mode; for (i = 1; i <= m; i++) print order[i], n[order[i]] }
' "$@" >"$merged"

MIN_PER_PACKAGE=100.0
failed=0

# coverage_of PKG: the package's statement coverage from the merged profile, as go test prints it.
coverage_of() {
  awk -v p="$1" '
    NR == 1 { next }
    {
      file = $1; sub(/:.*/, "", file)
      dir = file; sub(/\/[^\/]*$/, "", dir)
      if (dir != p) next
      total += $2
      if ($3 > 0) covered += $2
    }
    END { if (total > 0) printf "%.1f", 100 * covered / total; else print "0.0" }
  ' "$merged"
}

for pkg in $(go list ./internal/... ./cmd/... ./tools/...); do
  # "does this package have tests" is asked of the package, not of go test's report: the wording
  # changed under Go 1.26 ("no test files" is gone), and a string match on it silently stopped
  # skipping the test-less harnesses and started gating them at 0.0%.
  if [ "$(go list -f '{{len .TestGoFiles}}{{len .XTestGoFiles}}' "$pkg" 2>/dev/null)" = "00" ]; then
    echo "::warning::$pkg has no test files"
    continue
  fi
  cov="$(coverage_of "$pkg")"
  # A TEST-SUPPORT package cannot meet this bar, and demanding it is asking for tests of the
  # tests: the only statements left uncovered are its own failure and skip branches (t.Skip,
  # t.Fatal, t.Errorf), which run when a test has already failed. The exemption is derived from
  # a property - does the package import "testing" in non-test source? - rather than listed by
  # name, so it cannot be widened by adding a name and cannot go stale when a directory moves.
  # It matches exactly one package today: tools/clitest.
  dir="$(go list -f '{{.Dir}}' "$pkg")"
  if [ -n "$(grep -l '"testing"' "$dir"/*.go 2>/dev/null | grep -v '_test.go')" ]; then
    printf '%-52s %s%% (test-support: its own failure branches)\n' "$pkg" "$cov"
    continue
  fi
  printf '%-52s %s%%\n' "$pkg" "$cov"
  if ! awk -v c="$cov" -v m="$MIN_PER_PACKAGE" 'BEGIN { exit !(c >= m) }'; then
    echo "::error::$pkg coverage ${cov}% is below the ${MIN_PER_PACKAGE}% gate"
    failed=1
  fi
done

printf 'aggregate: '
go tool cover -func="$merged" | tail -1
[ "$failed" -eq 0 ]
