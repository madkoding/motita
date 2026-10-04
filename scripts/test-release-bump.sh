#!/usr/bin/env bash
# Checks that Conventional Commits map to the right semver bump.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -d node_modules ] || npm ci
node scripts/test-release-bump.mjs
