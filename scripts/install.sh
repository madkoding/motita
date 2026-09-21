#!/bin/sh
# starlight installer — one line, no Go, no Docker, no runtime on the target.
#
#   curl -fsSL https://raw.githubusercontent.com/madkoding/starlight/main/scripts/install.sh | sh
#
# It detects the system, downloads the matching static binary from the GitHub
# release, checks it against the release's SHA256SUMS, and puts it on the PATH.
#
# Overrides:
#   STARLIGHT_VERSION=v0.4.0      install a specific release instead of the latest
#   STARLIGHT_INSTALL_DIR=~/bin   install somewhere else
#
# Exit codes: 0 installed · 1 failed (nothing is left half-installed).
set -eu

REPO="madkoding/starlight"
BIN="starlight"
VERSION="${STARLIGHT_VERSION:-latest}"

say()  { printf '%s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
warn() { printf 'warning: %s\n' "$*" >&2; }

# --- 1. which system is this? -------------------------------------------------
#
# `uname -m` is not the same string on every system, and the 32-bit ARM kernel
# reports the chip it is running on (armv6l, armv7l, armv8l) rather than the
# architecture the binary is built for. All of them run the arm build, which is
# ARMv7, so they all map to `arm`.
detect_os() {
	case "$(uname -s)" in
		Linux)  echo linux ;;
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
FETCH="" ; CHECK=""
for c in curl wget; do
	command -v "$c" >/dev/null 2>&1 && { FETCH="$c"; break; }
done
[ -n "$FETCH" ] || die "neither curl nor wget is installed"

for c in sha256sum shasum; do
	command -v "$c" >/dev/null 2>&1 && { CHECK="$c"; break; }
done
[ -n "$CHECK" ] || die "neither sha256sum nor shasum is installed"

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

# --- 3. resolve the release ---------------------------------------------------
OS="$(detect_os)"
ARCH="$(detect_arch)"

case "$OS" in
	linux)  EXT="" ;;
	darwin) EXT="" ;;
esac
[ "$OS" = windows ] && EXT=".exe"

ASSET="${BIN}-${OS}-${ARCH}${EXT}"

if [ "$VERSION" = latest ]; then
	BASE="https://github.com/${REPO}/releases/latest/download"
else
	BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

say "starlight installer"
say "  system: ${OS}/${ARCH}"
say "  asset:  ${ASSET}"
say "  from:   ${VERSION}"

# --- 4. download into a temporary directory -----------------------------------
#
# Everything lands here first. The binary is moved into place only after it is
# downloaded AND verified, so a failure at any point leaves the system as it was.
TMP="$(mktemp -d "${TMPDIR:-/tmp}/starlight.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

if ! download "${BASE}/${ASSET}" "${TMP}/${ASSET}"; then
	rm -rf "$TMP"
	die "could not download ${ASSET} from ${VERSION}.
  The release may not publish this platform (linux/386, linux/amd64, linux/arm,
  linux/arm64, windows/386, windows/amd64, windows/arm64, darwin/amd64,
  darwin/arm64). Check ${BASE}/${ASSET}"
fi

# --- 5. verify against the release's own checksums ----------------------------
#
# The checksum comes from the same release as the binary, so this catches a
# corrupted or truncated download (a proxy, a flaky connection). It is not a
# defence against a compromised release: both files share one origin.
if download "${BASE}/SHA256SUMS" "${TMP}/SHA256SUMS" 2>/dev/null && [ -s "${TMP}/SHA256SUMS" ]; then
	expected="$(grep " ${ASSET}\$" "${TMP}/SHA256SUMS" | awk '{print $1}' | head -1)"
	if [ -z "$expected" ]; then
		warn "SHA256SUMS has no entry for ${ASSET}; the download could not be verified"
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
	warn "SHA256SUMS is not available in this release; the download could not be verified"
fi

# --- 6. install it somewhere on the PATH --------------------------------------
#
# A user-writable directory is preferred over /usr/local/bin: it keeps the
# installer from needing root, which is what lets it run from a pipe. If the
# chosen directory is not on the PATH, say so instead of failing: the install
# worked, and the user only needs to add it.
if [ -n "${STARLIGHT_INSTALL_DIR:-}" ]; then
	DIR="$STARLIGHT_INSTALL_DIR"
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

case ":${PATH}:" in
	*":${DIR}:"*) ;;
	*) warn "${DIR} is not on your PATH. Add it:
    export PATH=\"${DIR}:\$PATH\"" ;;
esac

say ""
say "Next: ${BIN} -init    (writes a configuration that works)"
