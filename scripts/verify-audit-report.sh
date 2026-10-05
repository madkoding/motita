#!/bin/sh
# Check an audit report is the kind of report an audit is supposed to produce.
#
# WHY THIS EXISTS
#
# An audit run by an agent ends in a document, and a document can sound complete without being
# true. The failure this guards against is the plausible one: findings with no location, a
# "Critical" with no way to fix it, a file path the model made up, a real token pasted into the
# report. The model may PROPOSE that the audit is done; this script is the anchor that decides.
#
# It checks STRUCTURE and CITATIONS, which are mechanical. It does not judge whether a finding is
# right - that is what the evidence and the reviewer are for - but a report that fails here cannot
# be right, and a report that cites a line that does not exist is fabricated.
#
# What it requires:
#   1. the report exists and is not empty;
#   2. it has Summary, Findings and Tooling run sections, and the summary says what was NOT covered
#      (a report that does not say so claims more than it measured);
#   3. every finding has a severity from the scale, a confidence, a location and a remediation, and
#      one carrying a CWE also has an exploit scenario;
#   4. every `path:line` in a Location exists under AUDIT_ROOT and the line is inside the file;
#   5. no live-looking secret is printed in clear;
#   6. the audit was read-only: no tracked file under AUDIT_ROOT was modified.
#
# Usage:
#   scripts/verify-audit-report.sh [report]        report defaults to $AUDIT_REPORT or audit-report.md
#   scripts/verify-audit-report.sh --selftest      prove the checks bite, on fixtures built here
# Environment: AUDIT_ROOT (default .) is the tree the report is about.
# Exit codes: 0 report accepted · 1 report rejected · 2 bad usage.
set -eu

# check REPORT: prints one FAIL line per problem and returns non-zero if there was any.
check() {
  report="$1"
  root="${AUDIT_ROOT:-.}"
  bad=0
  fail() { printf 'FAIL: %s\n' "$1"; bad=$((bad + 1)); }

  if [ ! -s "$report" ]; then
    fail "the report $report does not exist or is empty"
    return 1
  fi

  for section in "summary" "findings" "tooling run"; do
    grep -qiE "^##+ +$section" "$report" || fail "the report has no \"$section\" section"
  done
  grep -qiE 'not covered|out of scope|not in scope|everything in scope was covered' "$report" ||
    fail "the report never says what was NOT covered (or that everything in scope was)"

  # One record per finding: the lines from its "### " heading to the next heading. The records are
  # written as "FINDING<TAB>title<TAB>severity<TAB>confidence<TAB>locations<TAB>remediation<TAB>cwe<TAB>exploit".
  records="$(awk '
    function flush() {
      if (title != "") printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", title, sev, conf, loc, rem, cwe, expl, ""
      title = ""; sev = ""; conf = ""; loc = ""; rem = 0; cwe = 0; expl = 0
    }
    /^## /  { flush(); in_findings = (tolower($0) ~ /^## +findings/); next }
    /^### / { flush(); if (in_findings) { title = $0; sub(/^### +/, "", title) } ; next }
    title == "" { next }
    {
      line = tolower($0)
      if (line ~ /severity/)         { s = $0; sub(/.*[Ss]everity\**:? */, "", s); sev = s }
      if (line ~ /confidence/)       { c = $0; sub(/.*[Cc]onfidence\**:? */, "", c); conf = c }
      if (line ~ /\*\*location/ || line ~ /^[-* ]*location:/) { l = $0; sub(/.*[Ll]ocation\**:? */, "", l); loc = loc " " l }
      if (line ~ /remediation|recommendation|how to fix/) rem = 1
      if ($0 ~ /CWE-[0-9]+/) cwe = 1
      if (line ~ /exploit scenario|attack scenario/) expl = 1
    }
    END { flush() }
  ' "$report")"

  nfind=0
  if [ -n "$records" ]; then
    tab="$(printf '\t')"
    old_ifs="$IFS"; IFS='
'
    for rec in $records; do
      IFS="$old_ifs"
      nfind=$((nfind + 1))
      title="$(printf '%s' "$rec" | cut -d"$tab" -f1)"
      sev="$(printf '%s' "$rec" | cut -d"$tab" -f2)"
      conf="$(printf '%s' "$rec" | cut -d"$tab" -f3)"
      loc="$(printf '%s' "$rec" | cut -d"$tab" -f4)"
      rem="$(printf '%s' "$rec" | cut -d"$tab" -f5)"
      cwe="$(printf '%s' "$rec" | cut -d"$tab" -f6)"
      exp="$(printf '%s' "$rec" | cut -d"$tab" -f7)"

      printf '%s' "$sev" | grep -qiE 'critical|high|medium|low|info' ||
        fail "finding \"$title\": no severity from the scale (Critical, High, Medium, Low, Info)"
      [ -n "$conf" ] || fail "finding \"$title\": no confidence"
      [ "$rem" = "1" ] || fail "finding \"$title\": no remediation"
      if [ "$cwe" = "1" ] && [ "$exp" != "1" ]; then
        fail "finding \"$title\": it names a CWE but gives no exploit scenario"
      fi

      cites="$(printf '%s' "$loc" | grep -oE '[A-Za-z0-9_./@-]+:[0-9]+' || true)"
      if [ -z "$cites" ]; then
        fail "finding \"$title\": no path:line location"
      else
        for cite in $cites; do
          file="${cite%:*}"; n="${cite##*:}"
          if [ ! -f "$root/$file" ]; then
            fail "finding \"$title\": cites $cite but $file does not exist under $root"
          elif [ "$n" -lt 1 ] || [ "$n" -gt "$(wc -l < "$root/$file")" ]; then
            fail "finding \"$title\": cites $cite but $file has only $(wc -l < "$root/$file") lines"
          fi
        done
      fi
    done
    IFS="$old_ifs"
  fi
  if [ "$nfind" -eq 0 ] && ! grep -qiE 'no findings|no issues found|nothing to report' "$report"; then
    fail "the report has no findings and does not say there were none"
  fi

  if grep -qE 'AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|ghp_[A-Za-z0-9]{36}|sk-[A-Za-z0-9_-]{20,}|xox[abp]-[A-Za-z0-9-]{10,}' "$report"; then
    fail "the report prints something that looks like a live secret; redact it and say it must be rotated"
  fi

  if git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    changed="$(git -C "$root" diff --name-only 2>/dev/null || true)"
    [ -z "$changed" ] || fail "the audit must be read-only but these tracked files changed: $(printf '%s' "$changed" | tr '\n' ' ')"
  fi

  if [ "$bad" -eq 0 ]; then
    printf 'AUDIT_REPORT_OK findings=%s\n' "$nfind"
    return 0
  fi
  return 1
}

# selftest builds a tiny tree and a report that must pass, then breaks the report one way at a time
# and demands each break is caught - a check that accepts everything would pass a happy-path test.
selftest() {
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  mkdir -p "$work/src"
  i=1; while [ "$i" -le 10 ]; do echo "line $i"; i=$((i + 1)); done > "$work/src/app.go"
  export AUDIT_ROOT="$work"

  cat > "$work/good.md" <<'REP'
# Audit report
## Summary
One High finding. Not covered: the web/ directory.
## Findings
### F1 Path traversal in file serving
- **Severity**: High
- **Category**: CWE-22
- **Confidence**: High, the guard was read
- **Location**: `src/app.go:4`
- **Exploit scenario**: a request with `..` reads files outside the root.
- **Remediation**: resolve symlinks and check containment on the resolved path.
## Tooling run
- go vet: ok
REP
  out="$(check "$work/good.md" 2>&1)" || { printf 'SELFTEST FAIL: the good report was rejected:\n%s\n' "$out"; return 1; }

  fails=0
  expect_reject() { # name, sed expression applied to the good report
    sed "$2" "$work/good.md" > "$work/bad.md"
    if check "$work/bad.md" >/dev/null 2>&1; then
      printf 'SELFTEST FAIL: accepted a report with %s\n' "$1"; fails=$((fails + 1))
    else
      printf '  ok  rejected: %s\n' "$1"
    fi
  }
  expect_reject "no remediation"        '/Remediation/d'
  expect_reject "no severity"           '/Severity/d'
  expect_reject "no location"           '/Location/d'
  expect_reject "a file that is not there" 's#src/app.go:4#src/nothing.go:4#'
  expect_reject "a line past the end"   's#src/app.go:4#src/app.go:99#'
  expect_reject "a CWE and no exploit scenario" '/Exploit scenario/d'
  expect_reject "no coverage statement" 's/Not covered: the web\/ directory\.//'
  expect_reject "no tooling section"    '/## Tooling run/d'
  expect_reject "a live-looking secret" 's/- go vet: ok/- found AKIAABCDEFGHIJKLMNOP in config/'
  : > "$work/empty.md"
  if check "$work/empty.md" >/dev/null 2>&1; then
    printf 'SELFTEST FAIL: accepted an empty report\n'; fails=$((fails + 1))
  else
    printf '  ok  rejected: an empty report\n'
  fi

  [ "$fails" -eq 0 ] || return 1
  printf 'SELFTEST OK: the good report passes and every defect is caught\n'
}

case "${1:-}" in
  --selftest) selftest ;;
  -h|--help) sed -n '2,32p' "$0" ;;
  *) check "${1:-${AUDIT_REPORT:-audit-report.md}}" ;;
esac
