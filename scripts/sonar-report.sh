#!/usr/bin/env bash
# Prints the Sonar quality gate, key measures, hotspots and open issues as
# Markdown; the long issue list goes last.
# Needs SONAR_HOST_URL and SONAR_TOKEN; the project key comes from sonar-project.properties.
set -euo pipefail

: "${SONAR_HOST_URL:?}" "${SONAR_TOKEN:?}"
key="$(sed -n 's/^sonar.projectKey=//p' sonar-project.properties)"
api() { curl -fsS -u "${SONAR_TOKEN}:" "${SONAR_HOST_URL%/}/api/$1"; }

echo "## Sonar"
echo
echo "### Quality gate"
api "qualitygates/project_status?projectKey=${key}" | jq -r '
  "**\(.projectStatus.status)**", "",
  "| condition | actual | threshold | status |", "|---|---|---|---|",
  (.projectStatus.conditions[] | "| \(.metricKey) | \(.actualValue // "-") | \(.comparator) \(.errorThreshold) | \(.status) |")'

echo
echo "### Measures"
metrics=bugs,vulnerabilities,security_hotspots,code_smells,coverage,duplicated_lines_density,reliability_rating,security_rating,sqale_rating,security_review_rating,ncloc
api "measures/component?component=${key}&metricKeys=${metrics}" | jq -r '
  "| metric | value |", "|---|---|",
  (.component.measures | sort_by(.metric)[] | "| \(.metric) | \(.value) |")'

echo
echo "### Security hotspots to review"
echo
echo '```'
{ api "hotspots/search?project=${key}&status=TO_REVIEW&ps=500" | jq -r '
  .hotspots[] | "\(.vulnerabilityProbability)\t\(.ruleKey)\t\(.component | sub("^[^:]*:"; "")):\(.line // 0)\t\(.message)"'; } \
  || echo "(the token cannot list hotspots)"
echo '```'

echo
echo "### Open issues"
echo
echo '```'
page=1
while :; do
  body="$(api "issues/search?componentKeys=${key}&resolved=false&ps=500&p=${page}")"
  jq -r '.issues[] | "\(.type)\t\(.severity)\t\(.rule)\t\(.component | sub("^[^:]*:"; "")):\(.line // 0)\t\(.message)"' <<<"$body"
  total="$(jq '.paging.total' <<<"$body")"
  (( page * 500 < total && page < 20 )) || break
  page=$((page + 1))
done
echo '```'
