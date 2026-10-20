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
printf '#!/bin/sh\necho motita android arm64\n' > "$WORK/rel/motita-android-arm64"
# "latest" is the release the API names, v9.9.9. Both URL shapes serve the same files.
printf '{\n  "tag_name": "v9.9.9",\n  "name": "v9.9.9"\n}\n' > "$WORK/rel/latest.json"
ln -s . "$WORK/rel/v9.9.9"
ln -s . "$WORK/rel/latest"
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

# The embedded release key is blanked: the mock release cannot be signed by it, so the
# unsigned cases run keyless and case 5 installs a throwaway test key of its own.
sed "s#https://github.com/\${REPO}/releases/latest/download#http://127.0.0.1:$PORT/latest#; \
     s#https://github.com/\${REPO}/releases/download/\${VERSION}#http://127.0.0.1:$PORT/\${VERSION}#; \
     s#https://api.github.com/repos/\${REPO}/releases/latest#http://127.0.0.1:$PORT/latest.json#; \
     s#^RELEASE_PUBLIC_KEY=\".*\"#RELEASE_PUBLIC_KEY=\"\"#" \
  "$REPO/scripts/install.sh" > "$WORK/install.sh"
[ "$(grep -c "http://127.0.0.1:$PORT" "$WORK/install.sh")" = 3 ] || fail "the release URLs were not all redirected"
grep -q '^RELEASE_PUBLIC_KEY=""' "$WORK/install.sh" || fail "the embedded release key was not blanked"

export MOTITA_INSTALL_DIR="$WORK/dest"
# The installer writes the PATH into the shell's rc file: a home of its own keeps it
# away from the real one of whoever runs this.
# XDG_CONFIG_HOME and ZDOTDIR move fish's and zsh's files, and CI runners set the first.
export HOME="$WORK/home" SHELL=/bin/bash XDG_CONFIG_HOME="$WORK/home/.config"
unset ZDOTDIR || true
mkdir -p "$HOME"
unset MOTITA_VERSION MOTITA_INSECURE_SKIP_VERIFY || true
run() { sh "$WORK/install.sh" 2>&1; }

# --- 2. the happy path -------------------------------------------------------
out="$(run)" || fail "the installer failed on a good release: $out"
printf '%s\n' "$out" | grep -q 'checksum: ok' || fail "no verified checksum reported: $out"
printf '%s\n' "$out" | grep -q 'gh attestation verify' || fail "no provenance hint for an unsigned install: $out"
[ -x "$WORK/dest/motita" ] || fail "nothing was installed"
echo "the installer verifies and installs a good release"

# --- 2b. "latest" is pinned to one tag for every file -------------------------
# GitHub resolves releases/latest/download on each request, so a release published
# mid-install mixed two releases and read as a corrupt download.
printf '%s\n' "$out" | grep -q 'from:   v9.9.9' || fail "latest was not resolved to the API's tag: $out"
grep -q 'GET /latest/' "$WORK/httpd.log" && fail "a file was fetched through the per-request latest URL"
grep -q 'GET /v9.9.9/SHA256SUMS ' "$WORK/httpd.log" || fail "SHA256SUMS was not fetched from the pinned tag: $(cat "$WORK/httpd.log")"
mv "$WORK/rel/latest.json" "$WORK/latest.json.away"
out="$(run)" || fail "the installer failed when the API does not answer: $out"
printf '%s\n' "$out" | grep -q 'from:   latest' || fail "no fallback to the latest URL: $out"
mv "$WORK/latest.json.away" "$WORK/rel/latest.json"
echo "the installer takes every file from one tag, and falls back when the API is down"

# --- 2a. a directory off the PATH is added to the shell's rc, once ----------
printf '%s\n' "$out" | grep -q "added $WORK/dest to the PATH in $HOME/.bashrc" || fail "the PATH was not added to .bashrc: $out"
printf '%s\n' "$out" | grep -q "Next: $WORK/dest/motita config" || fail "the last line does not name the full path: $out"
grep -qxF "export PATH=\"$WORK/dest:\$PATH\"" "$HOME/.bashrc" || fail ".bashrc does not export the directory: $(cat "$HOME/.bashrc")"
run >/dev/null || fail "a second install failed"
[ "$(grep -c 'added by the motita installer' "$HOME/.bashrc")" = 1 ] || fail "a second install wrote the PATH again: $(cat "$HOME/.bashrc")"
( . "$HOME/.bashrc" && command -v motita >/dev/null ) || fail "a shell that reads .bashrc does not find motita"
out="$(SHELL=/usr/bin/zsh run)" || fail "the installer failed for zsh: $out"
grep -qF 'added by the motita installer' "$HOME/.zshrc" || fail "zsh's rc was not written: $out"
out="$(SHELL=/usr/bin/fish run)" || fail "the installer failed for fish: $out"
grep -qxF "fish_add_path -g \"$WORK/dest\"" "$HOME/.config/fish/conf.d/motita.fish" || fail "fish's config was not written: $out"
rm -f "$HOME/.profile"
out="$(SHELL=/bin/dash MOTITA_NO_MODIFY_PATH=1 run)" || fail "the installer failed with MOTITA_NO_MODIFY_PATH: $out"
[ ! -e "$HOME/.profile" ] || fail "MOTITA_NO_MODIFY_PATH=1 still wrote $HOME/.profile"
printf '%s\n' "$out" | grep -q 'is not on your PATH' || fail "with MOTITA_NO_MODIFY_PATH the user is not told to add it: $out"
echo "a directory off the PATH is added to the shell's rc once, and only when allowed"

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

# --- 4a. with a key but no OpenSSL 3, fail closed unless overridden ----------
# It used to skip the signature and install on the checksum alone. An openssl
# without -rawin (LibreSSL, OpenSSL 1.1) stands in for a missing one.
rm -rf "$WORK/dest" "$WORK/nossl"
mkdir -p "$WORK/nossl"
printf '#!/bin/sh\nexit 1\n' > "$WORK/nossl/openssl"
chmod +x "$WORK/nossl/openssl"
sed 's#^RELEASE_PUBLIC_KEY=""#RELEASE_PUBLIC_KEY="placeholder-key"#' "$WORK/install.sh" > "$WORK/install-keyed.sh"
grep -q '^RELEASE_PUBLIC_KEY="placeholder-key"' "$WORK/install-keyed.sh" || fail "the placeholder key did not land"
nossl() { PATH="$WORK/nossl:$PATH" sh "$WORK/install-keyed.sh" 2>&1; }
if out="$(nossl)"; then fail "the installer SUCCEEDED without OpenSSL 3 while a key is set"; fi
printf '%s\n' "$out" | grep -q 'no OpenSSL 3' || fail "the refusal does not name OpenSSL: $out"
printf '%s\n' "$out" | grep -q 'MOTITA_INSECURE_SKIP_VERIFY=1' || fail "the refusal does not name the override: $out"
[ ! -e "$WORK/dest/motita" ] || fail "a refused install left a binary behind"
out="$(MOTITA_INSECURE_SKIP_VERIFY=1 nossl)" || fail "the override did not install without OpenSSL: $out"
printf '%s\n' "$out" | grep -q 'UNVERIFIED' || fail "the override did not warn: $out"
echo "without OpenSSL 3 the installer refuses a keyed release unless MOTITA_INSECURE_SKIP_VERIFY=1"

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

# --- 6a. a failed copy keeps the previous install ----------------------------
# The download sits in $TMP, often another filesystem (a tmpfs /tmp). mv across
# filesystems deletes the target first and then copies, so a full disk left no
# motita at all. The fakes play a full disk: mv between two directories deletes
# the target and writes half a file, and cp writes half a file. Both then fail.
# Case 5 may have left a signature by another key behind; sign with the right one again.
[ -f "$WORK/key.pem" ] && openssl pkeyutl -sign -inkey "$WORK/key.pem" -rawin \
  -in "$WORK/rel/SHA256SUMS" -out "$WORK/rel/SHA256SUMS.sig"
rm -rf "$WORK/dest" "$WORK/fullbin"
mkdir -p "$WORK/dest" "$WORK/fullbin"
printf '#!/bin/sh\necho old build\n' > "$WORK/dest/motita"
chmod +x "$WORK/dest/motita"
cat > "$WORK/fullbin/mv" <<EOF
#!/bin/sh
[ "\$1" = -f ] && shift
if [ "\$(dirname "\$1")" != "\$(dirname "\$2")" ]; then
  rm -f "\$2"; head -c 8 "\$1" > "\$2"; echo "mv: No space left on device" >&2; exit 1
fi
exec "$(command -v mv)" -f "\$1" "\$2"
EOF
cat > "$WORK/fullbin/cp" <<'EOF'
#!/bin/sh
head -c 8 "$1" > "$2"; echo "cp: No space left on device" >&2; exit 1
EOF
chmod +x "$WORK/fullbin/mv" "$WORK/fullbin/cp"
if out="$(PATH="$WORK/fullbin:$PATH" sh "$WORK/install.sh" 2>&1)"; then fail "the installer SUCCEEDED on a full disk"; fi
printf '%s\n' "$out" | grep -q 'could not write' || fail "failed, but not on the copy: $out"
[ "$("$WORK/dest/motita" 2>&1)" = "old build" ] || fail "a failed copy did not keep the previous install: $out"
[ "$(ls -A "$WORK/dest")" = motita ] || fail "a failed copy left files behind: $(ls -A "$WORK/dest")"
echo "a failed copy into the install directory keeps the previous install"

# --- 7. Termux gets the android build, in $PREFIX/bin --------------------------
# A linux binary dies on Android with SIGSYS, so the installer must not hand one out
# there. uname is faked as an aarch64 Android, the way Termux reports itself.
rm -rf "$WORK/dest" "$WORK/fakebin" "$WORK/prefix"
mkdir -p "$WORK/fakebin" "$WORK/prefix/bin"
cat > "$WORK/fakebin/uname" <<'UNAME'
#!/bin/sh
case "$1" in
  -s) echo Linux ;;
  -m) echo aarch64 ;;
  -o) echo Android ;;
  *)  echo Linux ;;
esac
UNAME
chmod +x "$WORK/fakebin/uname"
out="$(env -u MOTITA_INSTALL_DIR PATH="$WORK/fakebin:$PATH" PREFIX="$WORK/prefix" \
  sh "$WORK/install.sh" 2>&1)" || fail "the installer failed on Android: $out"
printf '%s\n' "$out" | grep -q 'asset:  motita-android-arm64' || fail "Android was not given the android build: $out"
[ "$("$WORK/prefix/bin/motita")" = "motita android arm64" ] || fail "the android build did not land in \$PREFIX/bin: $out"
echo "on Termux the installer picks android/arm64 and installs into \$PREFIX/bin"

echo "VERDICT: the POSIX installer works end to end against a simulated release"
