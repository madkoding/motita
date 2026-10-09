#!/bin/sh
# Run the POSIX installer END TO END against a simulated release.
#
# The counterpart of verify-install-ps1.sh. It checks what a parse cannot: that
# the installer refuses a release it cannot verify (no SHA256SUMS, a checksum
# mismatch, a bad signature), that the explicit override still works, that a
# signed release verifies, and that a truncated download of the script runs
# nothing at all.
#
# The release is served over HTTP from a directory this script builds, so
# nothing is downloaded from GitHub. The only edits to the installer are its
# release base URL and, for the signature cases, its public key.
#
# Exit codes: 0 every case behaved · 1 a check failed.
set -eu

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${SH_PORT:-8898}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/motita-sh.XXXXXX")"
SRV_PID=""
cleanup() {
  rm -rf "$WORK"
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

fail() { printf 'error: %s\n' "$*"; exit 1; }

# --- 1. a release shaped like the real one -----------------------------------
mkdir -p "$WORK/rel"
for os in linux darwin; do
  for arch in 386 amd64 arm arm64; do
    printf '#!/bin/sh\necho motita %s %s\n' "$os" "$arch" > "$WORK/rel/motita-$os-$arch"
  done
done
sums() { (cd "$WORK/rel" && sha256sum motita-* | sort -k2 > SHA256SUMS); }
sums

(cd "$WORK/rel" && exec python3 -m http.server "$PORT" --bind 127.0.0.1) \
  >"$WORK/httpd.log" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 40); do
  curl -sf -o /dev/null "http://127.0.0.1:$PORT/SHA256SUMS" && break
  sleep 0.25
done
curl -sf -o /dev/null "http://127.0.0.1:$PORT/SHA256SUMS" || fail "the mock release is not being served on $PORT"

sed "s#https://github.com/\${REPO}/releases/latest/download#http://127.0.0.1:$PORT#" \
  "$REPO/scripts/install.sh" > "$WORK/install.sh"
grep -q "http://127.0.0.1:$PORT" "$WORK/install.sh" || fail "the release base was not redirected"

export MOTITA_INSTALL_DIR="$WORK/dest"
unset MOTITA_VERSION MOTITA_INSECURE_SKIP_VERIFY || true
run() { sh "$WORK/install.sh" 2>&1; }

# --- 2. the happy path -------------------------------------------------------
out="$(run)" || fail "the installer failed on a good release: $out"
printf '%s\n' "$out" | grep -q 'checksum: ok' || fail "no verified checksum reported: $out"
printf '%s\n' "$out" | grep -q 'gh attestation verify' || fail "no provenance hint for an unsigned install: $out"
[ -x "$WORK/dest/motita" ] || fail "nothing was installed"
echo "the installer verifies and installs a good release"

# --- 3. fail closed without SHA256SUMS, unless overridden --------------------
rm -rf "$WORK/dest"
mv "$WORK/rel/SHA256SUMS" "$WORK/SHA256SUMS.away"
if out="$(run)"; then fail "the installer SUCCEEDED without SHA256SUMS"; fi
printf '%s\n' "$out" | grep -q 'cannot be verified' || fail "the refusal does not say why: $out"
[ ! -e "$WORK/dest/motita" ] || fail "a refused install left a binary behind"
out="$(MOTITA_INSECURE_SKIP_VERIFY=1 run)" || fail "the override did not install: $out"
printf '%s\n' "$out" | grep -q 'UNVERIFIED' || fail "the override did not warn: $out"
mv "$WORK/SHA256SUMS.away" "$WORK/rel/SHA256SUMS"
echo "the installer refuses an unverifiable release unless MOTITA_INSECURE_SKIP_VERIFY=1"

# --- 4. a checksum mismatch is refused even with the override ----------------
rm -rf "$WORK/dest"
sed -i 's/^[0-9a-f]\{64\}/0000000000000000000000000000000000000000000000000000000000000000/' "$WORK/rel/SHA256SUMS"
if out="$(MOTITA_INSECURE_SKIP_VERIFY=1 run)"; then fail "the installer SUCCEEDED on a corrupt checksum"; fi
printf '%s\n' "$out" | grep -q 'checksum mismatch' || fail "failed, but not on the checksum: $out"
[ ! -e "$WORK/dest/motita" ] || fail "a refused install left a binary behind"
sums
echo "the installer refuses a checksum mismatch, override or not"

# --- 5. the signature, when a key is configured and OpenSSL 3 is present ------
if command -v openssl >/dev/null 2>&1 && openssl pkeyutl -help 2>&1 | grep -q -- -rawin; then
  openssl genpkey -algorithm ed25519 -out "$WORK/key.pem" 2>/dev/null
  pub="$(openssl pkey -in "$WORK/key.pem" -pubout | sed '1d;$d' | tr -d '\n')"
  sed -i "s#^RELEASE_PUBLIC_KEY=\"\"#RELEASE_PUBLIC_KEY=\"$pub\"#" "$WORK/install.sh"
  grep -q "^RELEASE_PUBLIC_KEY=\"$pub\"" "$WORK/install.sh" || fail "the test key did not land in the installer"

  rm -rf "$WORK/dest"
  if out="$(run)"; then fail "the installer SUCCEEDED without SHA256SUMS.sig while a key is set"; fi
  printf '%s\n' "$out" | grep -q 'SHA256SUMS.sig' || fail "the refusal does not name the signature: $out"

  openssl pkeyutl -sign -inkey "$WORK/key.pem" -rawin -in "$WORK/rel/SHA256SUMS" -out "$WORK/rel/SHA256SUMS.sig"
  out="$(run)" || fail "the installer failed on a signed release: $out"
  printf '%s\n' "$out" | grep -q 'signature: ok' || fail "no verified signature reported: $out"

  rm -rf "$WORK/dest"
  openssl genpkey -algorithm ed25519 -out "$WORK/other.pem" 2>/dev/null
  openssl pkeyutl -sign -inkey "$WORK/other.pem" -rawin -in "$WORK/rel/SHA256SUMS" -out "$WORK/rel/SHA256SUMS.sig"
  if out="$(MOTITA_INSECURE_SKIP_VERIFY=1 run)"; then fail "the installer SUCCEEDED on a signature by another key"; fi
  printf '%s\n' "$out" | grep -q 'does not verify' || fail "failed, but not on the signature: $out"
  [ ! -e "$WORK/dest/motita" ] || fail "a refused install left a binary behind"
  echo "the installer verifies the SHA256SUMS signature and refuses a forged one"
else
  echo "skip: the signature cases need OpenSSL 3 with ed25519"
fi

# --- 6. a truncated script runs nothing --------------------------------------
rm -rf "$WORK/dest"
half="$(($(wc -c < "$REPO/scripts/install.sh") / 2))"
head -c "$half" "$WORK/install.sh" > "$WORK/truncated.sh"
out="$(sh "$WORK/truncated.sh" 2>&1)" || true
[ ! -e "$WORK/dest/motita" ] || fail "a truncated installer installed something"
printf '%s\n' "$out" | grep -q 'motita installer' && fail "a truncated installer started running: $out"
echo "a truncated download of the installer runs nothing"

echo "VERDICT: the POSIX installer works end to end against a simulated release"
