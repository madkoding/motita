#!/bin/sh
# motita installer — one line, no Go, no Docker, no runtime on the target.
#
#   curl -fsSL https://motita.kodingvibes.com/install.sh | sh
#
# It detects the system, downloads the matching static binary from the GitHub
# release, checks it against the release's SHA256SUMS, and puts it on the PATH.
#
# Overrides:
#   MOTITA_VERSION=v0.4.0      install a specific release instead of the latest
#   MOTITA_INSTALL_DIR=~/bin   install somewhere else
#   MOTITA_INSECURE_SKIP_VERIFY=1
#                              install even when the release cannot be verified
#                              (no SHA256SUMS, no entry for this platform, no
#                              SHA256SUMS.sig, or no OpenSSL 3 to check it).
#                              A checksum MISMATCH or a BAD signature is refused
#                              regardless.
#   MOTITA_NO_MODIFY_PATH=1    do not add the install directory to the shell's
#                              rc file when it is not on the PATH
#
# Exit codes: 0 installed · 1 failed (nothing is left half-installed).
#
# Everything runs from main(), called on the last line: if the download of this
# script is cut off halfway, sh sees an unfinished function and runs nothing,
# instead of executing whatever prefix arrived.
#
# This file is published TWICE: here, and as site/install.sh, which is the URL
# above — GitHub Pages uploads site/ on its own and cannot see scripts/. The two
# copies are held byte-identical by internal/policy's published-copy test, so
# edit this one and run:  cp scripts/install.sh site/install.sh
set -eu

REPO="madkoding/motita"
BIN="motita"
VERSION="${MOTITA_VERSION:-latest}"

# The ed25519 key releases sign SHA256SUMS with, as the base64 body of its PEM
# public key. It is the same value as ReleasePublicKey in internal/updater, and
# scripts/release-signing-key.sh prints it. Empty is the placeholder: no key has
# been configured yet, and the signature is not checked.
RELEASE_PUBLIC_KEY="MCowBQYDK2VwAyEASjFxVSWdJ8wOLF06ooBU3hDEWtR+aRwNbRAog83ZW1c="

say()  { printf '%s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
warn() { printf 'warning: %s\n' "$*" >&2; }

# --- 1. which system is this? -------------------------------------------------
#
# `uname -m` is not the same string on every system, and the 32-bit ARM kernel
# reports the chip it is running on (armv6l, armv7l, armv8l) rather than the
# architecture the binary is built for. All of them run the arm build, which is
# ARMv7, so they all map to `arm`.
# Android (Termux) reports Linux in `uname -s`, but a GOOS=linux binary dies there
# with SIGSYS: Android's seccomp policy kills the process on syscalls such as
# faccessat2, which the Go linux runtime calls and the android one avoids. So
# Android is told apart by `uname -o` (or Termux's own variable) and gets the
# android build.
is_android() {
	[ -n "${TERMUX_VERSION:-}" ] && return 0
	[ "$(uname -o 2>/dev/null)" = Android ]
}

detect_os() {
	case "$(uname -s)" in
		Linux)  if is_android; then echo android; else echo linux; fi ;;
		Darwin) echo darwin ;;
		*)      die "unsupported system: $(uname -s). Only Linux and macOS are supported; on Windows use the .exe asset from the release page." ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		i386|i486|i586|i686|x86) echo 386 ;;
		x86_64|amd64)            echo amd64 ;;
		armv5*|armv6*|armv7*|armv8l|arm) echo arm ;;
		aarch64|arm64)           echo arm64 ;;
		*)                       die "unsupported architecture: $(uname -m)" ;;
	esac
}

# --- 2. a downloader and a checksum tool must exist ---------------------------
find_tools() {
	FETCH="" ; CHECK=""
	for c in curl wget; do
		command -v "$c" >/dev/null 2>&1 && { FETCH="$c"; break; }
	done
	[ -n "$FETCH" ] || die "neither curl nor wget is installed"

	for c in sha256sum shasum; do
		command -v "$c" >/dev/null 2>&1 && { CHECK="$c"; break; }
	done
	[ -n "$CHECK" ] || die "neither sha256sum nor shasum is installed"
}

# One function, so the two tools cannot drift apart in how they fetch.
download() {
	# download <url> <destination>
	if [ "$FETCH" = curl ]; then
		curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"
	else
		wget -q --tries=3 --timeout=15 -O "$2" "$1"
	fi
}

sha256_of() {
	# sha256_of <file> -> hex digest only
	if [ "$CHECK" = sha256sum ]; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

# A release that cannot be verified is refused unless the user opted out. This
# used to be a warning, and an install that went ahead anyway.
unverified() {
	if [ "${MOTITA_INSECURE_SKIP_VERIFY:-}" = 1 ]; then
		warn "$1; installing UNVERIFIED because MOTITA_INSECURE_SKIP_VERIFY=1"
	else
		die "$1, so the download cannot be verified. Nothing was installed.
  To install anyway (not recommended): MOTITA_INSECURE_SKIP_VERIFY=1"
	fi
}

# --- 5a. the signature over SHA256SUMS ----------------------------------------
#
# A checksum published beside the binary only proves the download is intact;
# whoever could replace the binary could replace SHA256SUMS too. The signature
# is what ties the checksums to the release key. It needs OpenSSL 3 (ed25519 and
# -rawin). Without it the install is refused, like any other release that cannot
# be verified: skipping the check quietly left only the checksum, which proves
# nothing against whoever replaced the release. Before a key is configured the
# check is skipped and the installer says how to check the build provenance.
verify_signature() {
	SIGNED=0
	if [ -z "$RELEASE_PUBLIC_KEY" ]; then
		say "  signature: not checked (this installer carries no release key yet)"
		return 0
	fi
	if ! command -v openssl >/dev/null 2>&1 || ! openssl pkeyutl -help 2>&1 | grep -q -- -rawin; then
		say "  signature: needs OpenSSL 3 with ed25519. Install it and run the installer again"
		say "    (Debian/Ubuntu: apt install openssl; macOS: brew install openssl@3, first on the PATH;"
		say "    Termux: pkg install openssl)"
		unverified "no OpenSSL 3 was found to check the release signature"
		return 0
	fi
	if ! download "${BASE}/SHA256SUMS.sig" "${TMP}/SHA256SUMS.sig" 2>/dev/null || [ ! -s "${TMP}/SHA256SUMS.sig" ]; then
		unverified "SHA256SUMS.sig is not available in this release"
		return 0
	fi
	printf -- '-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----\n' "$RELEASE_PUBLIC_KEY" > "${TMP}/release.pub"
	if ! openssl pkeyutl -verify -pubin -inkey "${TMP}/release.pub" -rawin \
		-in "${TMP}/SHA256SUMS" -sigfile "${TMP}/SHA256SUMS.sig" >/dev/null 2>&1; then
		die "SHA256SUMS.sig does not verify against the release key.
  The release may have been tampered with. Nothing was installed."
	fi
	SIGNED=1
	say "  signature: ok"
}

# --- 6a. put the directory on the PATH for the next shells ----------------------
#
# A piped installer runs in a CHILD shell: an export here dies with it, so the
# shell the user typed into cannot be changed from here. What can be changed is
# the file that shell reads when it starts, so the next terminal finds motita.
# One block, marked, written once: running the installer again adds nothing.
# MOTITA_NO_MODIFY_PATH=1 leaves every file alone.
PATH_MARK="# added by the motita installer"

shell_rc() {
	case "$(basename "${SHELL:-sh}")" in
		zsh)  echo "${ZDOTDIR:-$HOME}/.zshrc" ;;
		bash)
			# macOS opens login shells, which read .bash_profile and not .bashrc.
			if [ "$(uname -s)" = Darwin ]; then echo "${HOME}/.bash_profile"; else echo "${HOME}/.bashrc"; fi ;;
		fish) echo "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/motita.fish" ;;
		*)    echo "${HOME}/.profile" ;;
	esac
}

add_to_path() {
	# add_to_path <dir>: prints the file it wrote to, or nothing when it left them alone.
	[ "${MOTITA_NO_MODIFY_PATH:-}" = 1 ] && return 0
	rc="$(shell_rc)"
	if [ -f "$rc" ] && grep -qF "$PATH_MARK" "$rc" 2>/dev/null; then
		echo "$rc"
		return 0
	fi
	mkdir -p "$(dirname "$rc")" 2>/dev/null || return 0
	case "$rc" in
		*.fish) line="fish_add_path -g \"$1\"" ;;
		*)      line="export PATH=\"$1:\$PATH\"" ;;
	esac
	printf '\n%s\n%s\n' "$PATH_MARK" "$line" >> "$rc" 2>/dev/null || return 0
	echo "$rc"
}

main() {
find_tools

# --- 3. resolve the release ---------------------------------------------------
OS="$(detect_os)"
ARCH="$(detect_arch)"

# Only arm64 is published for Android: it is the one android target Go links
# without cgo, and every phone Termux still supports is 64-bit ARM.
if [ "$OS" = android ] && [ "$ARCH" != arm64 ]; then
	die "unsupported Android architecture: ${ARCH}. Only android/arm64 is published."
fi

EXT=""

ASSET="${BIN}-${OS}-${ARCH}${EXT}"

if [ "$VERSION" = latest ]; then
	BASE="https://github.com/${REPO}/releases/latest/download"
else
	BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

say "motita installer"
say "  system: ${OS}/${ARCH}"
say "  asset:  ${ASSET}"
say "  from:   ${VERSION}"

# --- 4. download into a temporary directory -----------------------------------
#
# Everything lands here first. The binary is moved into place only after it is
# downloaded AND verified, so a failure at any point leaves the system as it was.
TMP="$(mktemp -d "${TMPDIR:-/tmp}/motita.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

if ! download "${BASE}/${ASSET}" "${TMP}/${ASSET}"; then
	rm -rf "$TMP"
	die "could not download ${ASSET} from ${VERSION}.
  The release may not publish this platform (linux/386, linux/amd64, linux/arm,
  linux/arm64, android/arm64, windows/386, windows/amd64, windows/arm64,
  darwin/amd64, darwin/arm64). Check ${BASE}/${ASSET}"
fi

# --- 5. verify against the release's own checksums ----------------------------
#
# The checksum comes from the same release as the binary, so on its own it
# catches a corrupted or truncated download (a proxy, a flaky connection). The
# signature (5a) is what makes it a defence against a compromised release.
SIGNED=0
if download "${BASE}/SHA256SUMS" "${TMP}/SHA256SUMS" 2>/dev/null && [ -s "${TMP}/SHA256SUMS" ]; then
	verify_signature
	expected="$(grep " ${ASSET}\$" "${TMP}/SHA256SUMS" | awk '{print $1}' | head -1)"
	if [ -z "$expected" ]; then
		unverified "SHA256SUMS has no entry for ${ASSET}"
	else
		actual="$(sha256_of "${TMP}/${ASSET}")"
		if [ "$expected" != "$actual" ]; then
			rm -rf "$TMP"
			die "checksum mismatch for ${ASSET}
  expected ${expected}
  got      ${actual}
  The download is corrupt. Nothing was installed."
		fi
		say "  checksum: ok"
	fi
else
	unverified "SHA256SUMS is not available in this release"
fi

# --- 6. install it somewhere on the PATH --------------------------------------
#
# A user-writable directory is preferred over /usr/local/bin: it keeps the
# installer from needing root, which is what lets it run from a pipe. If the
# chosen directory is not on the PATH, say so instead of failing: the install
# worked, and the user only needs to add it. Termux has no /usr/local/bin; its
# own $PREFIX/bin is user-writable and already on the PATH.
if [ -n "${MOTITA_INSTALL_DIR:-}" ]; then
	DIR="$MOTITA_INSTALL_DIR"
elif [ "$OS" = android ] && [ -n "${PREFIX:-}" ] && [ -w "${PREFIX}/bin" ]; then
	DIR="${PREFIX}/bin"
elif [ -w /usr/local/bin ] 2>/dev/null; then
	DIR=/usr/local/bin
else
	DIR="${HOME}/.local/bin"
fi

mkdir -p "$DIR" || die "could not create ${DIR}"
chmod +x "${TMP}/${ASSET}"
mv -f "${TMP}/${ASSET}" "${DIR}/${BIN}" || die "could not write ${DIR}/${BIN}"

say "  installed: ${DIR}/${BIN}"

# --- 7. prove it runs ---------------------------------------------------------
#
# Reporting success without executing the binary would make this script unable
# to catch the one failure that matters here: a binary that does not run on this
# machine (the usual symptom of a wrong architecture).
if "${DIR}/${BIN}" -version >/dev/null 2>&1; then
	say "  verified:  it runs ($("${DIR}/${BIN}" -version 2>&1 | head -1))"
else
	warn "${DIR}/${BIN} was installed but does not run on this system"
fi

RUN="$BIN"
case ":${PATH}:" in
	*":${DIR}:"*) ;;
	*)
		# Until a new shell starts, the full path is what works, so that is what
		# the last line tells the user to type.
		RUN="${DIR}/${BIN}"
		rc="$(add_to_path "$DIR")"
		if [ -n "$rc" ]; then
			say "  path:      added ${DIR} to the PATH in ${rc}"
			say "             open a new terminal, or run in this one:"
		else
			warn "${DIR} is not on your PATH. Add it:"
		fi
		say "    export PATH=\"${DIR}:\$PATH\""
		;;
esac

if [ "$SIGNED" != 1 ]; then
	say "  provenance: check where this binary was built with"
	say "    gh attestation verify ${DIR}/${BIN} --repo ${REPO}"
fi

say ""
say "Next: ${RUN} config    (writes a configuration that works)"
}

main "$@"
