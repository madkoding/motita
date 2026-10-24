#!/bin/sh
# Commit the embedded web UI that `npm run build` regenerates.
#
# WHY THIS EXISTS
#
# `web/vite.config.ts` writes the build straight into `internal/webui/assets`,
# the directory `go:embed` reads. Only `index.html` is tracked: it names the
# hashed bundles, so a stale copy points a release at files that no longer
# exist. Rebuilding by hand and forgetting `git add` leaves exactly that stale
# document in the tree, and nothing fails until a user loads the page.
#
# This runs the build and commits the regenerated document when it changed. On a
# clean tree the build is deterministic and produces no diff, so it exits 0
# without a commit: the script is safe to run at any time.
#
# It is a script and not an npm `postbuild` hook on purpose: the gate's
# package.json scripts are fixed, and a hook that commits on every build would
# surprise anyone running `npm run build` just to look at the output.
#
# Usage: scripts/commit-generated-assets.sh
# Exit codes: 0 nothing to commit, or the commit was made · 1 the build failed.
set -eu

root=$(git rev-parse --show-toplevel)
cd "$root"

if [ ! -d web/node_modules ]; then
  printf 'error: web/node_modules is missing; run `cd web && npm ci` first\n' >&2
  exit 1
fi

printf 'building the web UI...\n'
( cd web && npm run build )

asset=internal/webui/assets/index.html
if [ -z "$(git status --porcelain -- "$asset")" ]; then
  printf '%s is unchanged; nothing to commit\n' "$asset"
  exit 0
fi

git add -- "$asset"
git commit -m "build(webui): regenerate the embedded index.html"
printf 'committed %s\n' "$asset"
