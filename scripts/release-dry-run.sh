#!/usr/bin/env bash
# Shows the next version semantic-release would compute from the current HEAD.
# semantic-release needs the release branch on a remote, so HEAD and the tags are
# pushed to a throwaway local bare repo; nothing touches the real remote or GitHub.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
[ -d node_modules ] || npm ci
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
git init --bare -q -b main "$tmp/origin.git"
git push -q "$tmp/origin.git" HEAD:refs/heads/main 'refs/tags/*:refs/tags/*'
git clone -q "$tmp/origin.git" "$tmp/work"
cd "$tmp/work"
cat > .releaserc.json <<'EOF'
{
  "branches": ["main"],
  "tagFormat": "v${version}",
  "plugins": [
    ["@semantic-release/commit-analyzer", {"preset": "conventionalcommits"}],
    ["@semantic-release/release-notes-generator", {"preset": "conventionalcommits"}]
  ]
}
EOF
ln -s "$root/node_modules" node_modules
node node_modules/semantic-release/bin/semantic-release.js --dry-run --no-ci
