#!/bin/sh
# check-webui-assets.sh — fails when internal/webui/assets/index.html names a bundle that is not
# on disk beside it.
#
# Only index.html is tracked under internal/webui/assets; the bundles it names are gitignored.
# A commit can therefore carry a page pointing at files nobody has: the server answers 200 and
# the browser 404s its own script, which is a blank page with no error in any log. This is the
# same question assetsMissingFromDisk asks in internal/webui/page_test.go, asked before the
# commit instead of after the push.
#
# Usage: scripts/check-webui-assets.sh [page]
# Exit: 0 every reference is on disk · 1 a reference is missing · 2 the page is not there.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
page=${1:-$root/internal/webui/assets/index.html}
[ -f "$page" ] || { echo "check-webui-assets: no such page: $page" >&2; exit 2; }
dir=$(dirname -- "$page")

# src="..." and href="..." values that are local: a leading "/" and not "//" (another origin).
refs=$(grep -oE '(src|href)="/[^"]*"' "$page" \
  | sed -e 's/^[a-z]*="//' -e 's/"$//' \
  | grep -v '^//' \
  | sort -u) || true

missing=
for ref in $refs; do
  [ -f "$dir/${ref#/}" ] || missing="$missing $ref"
done

if [ -n "$missing" ]; then
  echo "check-webui-assets: index.html names assets that are not on disk:" >&2
  for ref in $missing; do echo "  $ref" >&2; done
  echo "run \`npm run build\` in web/ and commit internal/webui/assets/index.html together with the bundles it names" >&2
  exit 1
fi

echo "check-webui-assets: every asset index.html names is on disk"
