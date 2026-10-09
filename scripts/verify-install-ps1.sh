#!/bin/sh
# Run the Windows installer END TO END against a simulated release.
#
# Why a real execution and not a parse: the installer's whole job is a network round
# trip, a checksum, a file move and a PATH edit, and every one of those is invisible to a
# syntax check. The measured failure mode this exists for is `exit` inside `irm | iex`,
# which ends the CALLER's PowerShell session - a failed download would close the user's
# window - plus the ordinary ones: a wrong asset name, a checksum compared against the
# wrong file, an install that leaves a half-installed binary behind.
#
# The release is served over HTTP from a directory this script builds, so nothing is
# downloaded from GitHub and no release has to exist. pwsh is what GitHub's ubuntu-latest
# image already ships (PowerShell 7.6.6), which is why this runs on the same runner as
# everything else instead of needing a Windows job.
#
# PROCESSOR_ARCHITECTURE is set to AMD64 below ON PURPOSE: this script runs on Linux,
# where the variable does not exist, and the installer refuses to guess an architecture.
# Setting it is what makes the amd64 path - the one almost every Windows user takes -
# the path under test. The arm64 and 386 branches are covered by the same mechanism and
# are exercised by hand; see the comment on the amd64 assertion.
#
# Exit codes: 0 the installer installed the right file and refused a corrupt one
#             1 a check failed
#             2 cannot run here (no pwsh) - a skip, not a defect.
set -eu

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PWSH="${PWSH:-$(command -v pwsh || true)}"
[ -n "$PWSH" ] || PWSH="$(command -v powershell || true)"

# An explicit PWSH that is not there is a SKIP, not a failure: the usual reason is a
# machine without PowerShell, and reporting that as a broken installer would be a gate
# that lies about what it checked.
if [ -z "$PWSH" ] || ! command -v "$PWSH" >/dev/null 2>&1; then
  echo "SKIP: no pwsh (set PWSH=... to the one on this machine)"
  exit 2
fi

# --- 0. the static rules, FIRST ----------------------------------------------
# These run before anything else so that a violation names itself. The `exit` rule in
# particular is caught by the end-to-end run too - an exiting installer fails the happy
# path - but that reports "the installer failed on a good release", which sends the reader
# looking for a download problem instead of at the one line that is wrong.
#
# `exit` inside `irm | iex` ends the CALLER's session. Measured: the caller never regains
# control and its remaining lines never run. A failed download would close the user's
# window.
if grep -qE '^[[:space:]]*exit\b|\bexit [0-9]' "$REPO/scripts/install.ps1"; then
  echo "error: install.ps1 uses exit; under 'irm | iex' that closes the user's window"
  exit 1
fi
# ASCII only: it is fetched and evaluated in one process, and a non-ASCII byte is how a
# pipe-to-interpreter fails for the people who cannot debug it.
if LC_ALL=C grep -qP '[^\x00-\x7F]' "$REPO/scripts/install.ps1"; then
  echo "error: install.ps1 contains non-ASCII characters"
  exit 1
fi

PORT="${PS1_PORT:-8899}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/motita-ps1.XXXXXX")"
SRV_PID=""
cleanup() {
  rm -rf "$WORK"
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# --- 1. a release shaped exactly like the real one ---------------------------
mkdir -p "$WORK/rel"
for arch in 386 amd64 arm64; do
  printf 'MZ motita windows %s\n' "$arch" > "$WORK/rel/motita-windows-$arch.exe"
done
# Sorted by name, the way ci.yml builds it: the installer has to find its entry wherever
# in the file it sits.
(cd "$WORK/rel" && sha256sum * | sort -k2 > SHA256SUMS)

(cd "$WORK/rel" && exec python3 -m http.server "$PORT" --bind 127.0.0.1) \
  >"$WORK/httpd.log" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 40); do
  curl -sf -o /dev/null "http://127.0.0.1:$PORT/SHA256SUMS" && break
  sleep 0.25
done
curl -sf -o /dev/null "http://127.0.0.1:$PORT/SHA256SUMS" || {
  echo "error: the mock release is not being served on $PORT"
  exit 1
}

# --- 2. the installer, with only its release base redirected -----------------
# The ONLY edit is the base URL. Everything else - the asset name it builds, the checksum
# it compares, the directory it writes - is the script that ships.
sed "s#https://github.com/\$Repo/releases/latest/download#http://127.0.0.1:$PORT#; \
     s#https://github.com/\$Repo/releases/download/\$Version#http://127.0.0.1:$PORT#" \
  "$REPO/scripts/install.ps1" > "$WORK/install-mock.ps1"
if [ "$(grep -c "http://127.0.0.1:$PORT" "$WORK/install-mock.ps1")" -ne 2 ]; then
  echo "error: the release base was not redirected; this check is not testing what it says"
  exit 1
fi

# --- 3. the happy path -------------------------------------------------------
export MOTITA_INSTALL_DIR="$WORK/dest"
export PROCESSOR_ARCHITECTURE=AMD64
if ! out="$("$PWSH" -NoProfile -File "$WORK/install-mock.ps1" 2>&1)"; then
  echo "error: the installer failed on a good release:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
fi
printf '%s\n' "$out" | grep -q 'checksum: ok' || {
  echo "error: the installer did not report a verified checksum:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
}
# The RIGHT binary, not merely a binary: the mock files differ, so a wrong architecture is
# a wrong file. This is the assertion that the ARCHITEW6432 rule and the asset name are
# both right, and it is the reason a plausible-looking install counts for nothing.
if ! cmp -s "$WORK/rel/motita-windows-amd64.exe" "$WORK/dest/motita.exe"; then
  echo "error: the installed binary is not the amd64 asset the release carries"
  exit 1
fi
echo "the installer downloads, verifies and installs the amd64 asset"

# --- 3b. the 32-bit-PowerShell-on-64-bit-Windows case ------------------------
# 32-bit PowerShell reports x86 and the REAL architecture in ARCHITEW6432. Without that
# rule a 64-bit machine is handed the 32-bit binary, which RUNS - so the mistake is
# invisible until something needs the address space. Asserted because this is a rule that
# cannot be reasoned about from reading the code on Linux.
rm -rf "$WORK/dest"
if ! out="$(PROCESSOR_ARCHITECTURE=x86 PROCESSOR_ARCHITEW6432=AMD64 \
      "$PWSH" -NoProfile -File "$WORK/install-mock.ps1" 2>&1)"; then
  echo "error: the installer failed when a 32-bit PowerShell reports the real architecture:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
fi
if ! cmp -s "$WORK/rel/motita-windows-amd64.exe" "$WORK/dest/motita.exe"; then
  echo "error: 32-bit PowerShell on 64-bit Windows got the WRONG binary (expected amd64)"
  exit 1
fi
echo "the installer prefers ARCHITEW6432, so a 32-bit shell still gets the 64-bit binary"

# --- 4. the failure path, and that it leaves nothing behind ------------------
rm -rf "$WORK/dest"
sed -i 's/^[0-9a-f]\{64\}  motita-windows-amd64.exe/deadbeef00000000000000000000000000000000000000000000000000000000  motita-windows-amd64.exe/' \
  "$WORK/rel/SHA256SUMS"
grep -q '^deadbeef' "$WORK/rel/SHA256SUMS" || {
  echo "error: the corrupt checksum fixture did not land; the failure path is not being tested"
  exit 1
}
if out="$("$PWSH" -NoProfile -File "$WORK/install-mock.ps1" 2>&1)"; then
  echo "error: the installer SUCCEEDED on a corrupt checksum"
  exit 1
fi
printf '%s\n' "$out" | grep -q 'checksum mismatch' || {
  echo "error: the installer failed, but not because of the checksum:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
}
if [ -e "$WORK/dest/motita.exe" ]; then
  echo "error: a failed install left a binary behind"
  exit 1
fi
echo "the installer refuses a corrupt download and installs nothing"

# --- 4b. a release WITHOUT checksums is refused, unless overridden ------------
# It used to warn and install anyway: an unverifiable download went on the PATH.
rm -rf "$WORK/dest"
rm -f "$WORK/rel/SHA256SUMS"
if out="$("$PWSH" -NoProfile -File "$WORK/install-mock.ps1" 2>&1)"; then
  echo "error: the installer SUCCEEDED on a release without SHA256SUMS"
  exit 1
fi
printf '%s\n' "$out" | grep -q 'cannot be verified' || {
  echo "error: the installer failed, but not because the release is unverifiable:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
}
if [ -e "$WORK/dest/motita.exe" ]; then
  echo "error: a refused install left a binary behind"
  exit 1
fi
if ! out="$(MOTITA_INSECURE_SKIP_VERIFY=1 "$PWSH" -NoProfile -File "$WORK/install-mock.ps1" 2>&1)"; then
  echo "error: MOTITA_INSECURE_SKIP_VERIFY=1 did not let the install through:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
fi
printf '%s\n' "$out" | grep -q 'UNVERIFIED' || {
  echo "error: the override installed without saying the download is unverified"
  exit 1
}
echo "the installer refuses an unverifiable release unless MOTITA_INSECURE_SKIP_VERIFY=1"

# --- 5. and it stays runnable under irm | iex ---------------------------------
# The static rules ran at the top (step 0). This asserts the thing they cannot: that the
# SHIPPED script, invoked the way the site tells people to invoke it, installs the right
# file and leaves the caller's session alive afterwards.
#
# The checksums are rebuilt first: step 4 deliberately corrupted them, and running this
# against a corrupt release would test the failure path a second time.
(cd "$WORK/rel" && sha256sum * | sort -k2 > SHA256SUMS)
rm -rf "$WORK/dest"
cp "$WORK/install-mock.ps1" "$WORK/rel/payload.ps1"
if ! out="$(PROCESSOR_ARCHITECTURE=AMD64 "$PWSH" -NoProfile -Command '
      Write-Host "caller: BEFORE"
      irm "http://127.0.0.1:'"$PORT"'/payload.ps1" | iex
      Write-Host "caller: AFTER"' 2>&1)"; then
  echo "error: 'irm | iex' failed:"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
fi
printf '%s\n' "$out" | grep -q 'caller: AFTER' || {
  echo "error: the caller's session did NOT survive 'irm | iex'; the installer ended it"
  printf '%s\n' "$out" | sed 's/^/    /'
  exit 1
}
# Serving it as application/octet-stream is what GitHub Pages does for .ps1 (ps1 is absent
# from mime-db), so the pipe-to-interpreter path is exercised under the content type the
# real URL will use.
if ! cmp -s "$WORK/rel/motita-windows-amd64.exe" "$WORK/dest/motita.exe"; then
  echo "error: 'irm | iex' did not install the amd64 asset"
  exit 1
fi
echo "the installer works through 'irm | iex' and leaves the caller's session alive"

echo "VERDICT: the Windows installer works end to end against a simulated release"
