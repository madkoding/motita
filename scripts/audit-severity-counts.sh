#!/bin/sh
# audit-severity-counts.sh — counts the findings of the audit report by severity and keeps the
# table of section 9 in step with them.
#
# A finding is written once, as "- **A1 (alto) — title.**" or "### M4 (medio) — title", and may be
# mentioned again further down; the count is of distinct IDs, so a mention never counts twice.
#
# Usage:
#   scripts/audit-severity-counts.sh           print the counts
#   scripts/audit-severity-counts.sh --check   exit 1 if section 9 does not match the counts
#   scripts/audit-severity-counts.sh --write   rewrite section 9's table from the counts
# Environment: AUDIT_DOC (default docs/auditoria.md)
# Exit codes: 0 the counts were printed / the table matches · 1 the table is stale · 2 bad usage.
set -eu

doc="${AUDIT_DOC:-docs/auditoria.md}"
[ -s "$doc" ] || { echo "audit-severity-counts: no such report: $doc" >&2; exit 2; }

# The report is in Spanish and the only accented severity is "crítico": folding it keeps the
# parsing ASCII, which every awk and grep agrees on whatever the locale.
flat="$(sed -e 's/Crítico/Critico/g' -e 's/crítico/critico/g' "$doc")"

# One "ID severity" line per distinct finding, in ID order.
pairs="$(printf '%s\n' "$flat" | awk '
  {
    line = $0
    while (match(line, /[A-Z][0-9]+ \((critico|alto|medio|bajo)\)/)) {
      s = substr(line, RSTART, RLENGTH)
      id = s; sub(/ \(.*/, "", id)
      sev = s; sub(/^[^(]*\(/, "", sev); sub(/\)$/, "", sev)
      if (!(id in seen)) { seen[id] = sev; ids[++n] = id }
      line = substr(line, RSTART + RLENGTH)
    }
  }
  END { for (i = 1; i <= n; i++) print ids[i], seen[ids[i]] }
' | sort -k1,1V)"

count() { printf '%s\n' "$pairs" | awk -v s="$1" '$2 == s { n++ } END { print n + 0 }'; }
ids()   { printf '%s\n' "$pairs" | awk -v s="$1" '$2 == s { printf "%s%s", sep, $1; sep = ", " }'; }

label() {
  case "$1" in
    critico) echo "Crítico" ;;
    alto)    echo "Alto" ;;
    medio)   echo "Medio" ;;
    bajo)    echo "Bajo" ;;
  esac
}

total() {
  n=0
  for sev in critico alto medio bajo; do n=$((n + $(count "$sev"))); done
  echo "$n"
}

# The table's data rows, exactly as section 9 must carry them.
table() {
  for sev in critico alto medio bajo; do
    list="$(ids "$sev")"
    [ -n "$list" ] || list="—"
    printf '| %s | %s | %s |\n' "$(label "$sev")" "$(count "$sev")" "$list"
  done
  printf '| **Total** | **%s** | |\n' "$(total)"
}

# The data rows section 9 carries now.
current() {
  awk '
    /^## / { in9 = ($0 ~ /^## 9\./); next }
    in9 && /^\|/ {
      if ($0 ~ /^\|[ -]*-/) next
      if ($0 ~ /Severidad/) next
      print
    }
  ' "$doc"
}

print_counts() {
  for sev in critico alto medio bajo; do
    list="$(ids "$sev")"
    printf '%-8s %s%s\n' "$(label "$sev")" "$(count "$sev")" "${list:+ ($list)}"
  done
  printf '%-8s %s\n' "Total" "$(total)"
}

write_table() {
  new="$(table)"
  tmp="$doc.tmp.$$"
  trap 'rm -f "$tmp"' EXIT
  NEW="$new" awk '
    /^## / { in9 = ($0 ~ /^## 9\./); print; next }
    in9 && /^\|/ {
      if ($0 ~ /^\|[ -]*-/) { print; next }
      if ($0 ~ /Severidad/) { print; next }
      if (!done) { printf "%s\n", ENVIRON["NEW"]; done = 1 }
      next
    }
    { print }
  ' "$doc" > "$tmp"
  mv "$tmp" "$doc"
  trap - EXIT
}

case "${1:-}" in
  --check)
    print_counts
    if [ "$(current)" = "$(table)" ]; then
      echo "AUDIT_COUNTS_OK: section 9 matches the findings"
      exit 0
    fi
    echo "AUDIT_COUNTS_STALE: section 9 does not match the findings; run scripts/audit-severity-counts.sh --write" >&2
    exit 1
    ;;
  --write)
    write_table
    print_counts
    echo "AUDIT_COUNTS_WRITTEN: section 9 rewritten from the findings"
    ;;
  -h|--help)
    sed -n '2,15p' "$0"
    ;;
  "")
    print_counts
    ;;
  *)
    echo "audit-severity-counts: unknown argument: $1" >&2
    exit 2
    ;;
esac
