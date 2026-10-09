#!/bin/sh
# Generate the ed25519 key pair that signs every release's SHA256SUMS.
#
# Run it ONCE, by a maintainer, on a trusted machine (`make release-key`). It
# writes the pair OUTSIDE the repository and prints what to do with each half:
#
#   - the PRIVATE key becomes the RELEASE_SIGNING_KEY repository secret, which
#     the release job in .github/workflows/ci.yml signs SHA256SUMS with. It is
#     never committed (.gitignore also ignores *.pem and *.key);
#   - the PUBLIC key (one base64 line) is pasted into ReleasePublicKey in
#     internal/updater/updater.go, RELEASE_PUBLIC_KEY in scripts/install.sh and
#     $ReleasePublicKey in scripts/install.ps1 (then copied to site/).
#
# Until the public key is embedded, the updater and the installers keep the
# checksum-only behaviour; once it is, an unsigned release is refused.
#
# Usage: scripts/release-signing-key.sh [output-directory]
# Needs OpenSSL 3 (ed25519). Exit codes: 0 generated · 1 failed.
set -eu

out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/motita-release-key.XXXXXX")}"
repo="$(cd "$(dirname "$0")/.." && pwd)"
case "$(cd "$out" 2>/dev/null && pwd || echo "$out")/" in
	"$repo"/*) printf 'error: refusing to write a private key inside the repository (%s)\n' "$out" >&2; exit 1 ;;
esac

command -v openssl >/dev/null 2>&1 || { echo "error: openssl is not installed" >&2; exit 1; }
mkdir -p "$out"
umask 077
key="$out/release-signing-key.pem"
[ ! -e "$key" ] || { printf 'error: %s already exists; not overwriting a key\n' "$key" >&2; exit 1; }
openssl genpkey -algorithm ed25519 -out "$key"
pub="$(openssl pkey -in "$key" -pubout | sed '1d;$d' | tr -d '\n')"

cat <<EOF
Private key: $key
  Store it as the repository secret, then delete the file:
    gh secret set RELEASE_SIGNING_KEY < "$key"
    rm -f "$key"

Public key (paste it into the three files below, then commit):
  $pub

  internal/updater/updater.go   const ReleasePublicKey = "$pub"
  scripts/install.sh            RELEASE_PUBLIC_KEY="$pub"
  scripts/install.ps1           \$ReleasePublicKey = '$pub'
  cp scripts/install.sh site/install.sh && cp scripts/install.ps1 site/install.ps1
EOF
