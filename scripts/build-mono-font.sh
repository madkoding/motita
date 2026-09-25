#!/usr/bin/env bash
# Regenerate the mono font assets (JetBrains Mono Nerd Font) from the upstream
# release. Run from the repo root. Nothing here is checked in as source: the
# .woff2 files under web/public/ are the artefacts, and this is how they are made.
#
# WHY THE FONT IS SPLIT INTO THREE FILES
#
# A plain JetBrainsMonoNerdFont-Regular.ttf is 2.5 MB, because the Nerd Font
# patch adds 10,610 icon glyphs on top of the ~1,600 text ones. Two faces
# (Regular + Bold) carry the same 10,610 icons TWICE, so shipping both whole
# costs 1.8 MB of duplicated outlines.
#
# The icons are also not needed on every page: they matter for the glyphs a
# terminal tool prints, never for code or prose. So the icons are cut into ONE
# face that neither is Regular nor Bold — the browser falls back to it for the
# private-use codepoints only, and fetches it only if such a glyph is actually
# drawn. Code and prose fetch 108 KB of text faces and never touch the 910 KB.
#
#   JetBrainsMonoNerdFont-Regular.woff2    text, weight 400
#   JetBrainsMonoNerdFont-Bold.woff2       text, weight 700
#   JetBrainsMonoNerdFont-Icons.woff2      the 10,610 PUA glyphs, one copy
#
# Numbers measured on the 2026-09-25 run, with --no-hinting:
#   text regular   53.2 KB      full NF regular, woff2    961.9 KB
#   text bold      54.6 KB      full NF bold, woff2       963.4 KB
#   icons         910.4 KB      the two whole faces     1925.3 KB
#   ------------------------    split total             1018.2 KB
#
# `--no-hinting` is worth it: hinting is for low-DPI bitmap rendering, these
# faces are used at UI sizes on modern displays, and dropping it saves ~35% on
# the text faces. Re-add it if small-size crispness on a bad screen matters more.
#
# Requires: fonttools + brotli with pyftsubset on PATH (uv tool install fonttools
# installs it into its own venv; `uv tool run --from fonttools pyftsubset` works too).
set -euo pipefail

VER="${1:-v3.5.1}"
OUT_DIR="${2:-web/public}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "Fetching ryanoasis/nerd-fonts $VER (JetBrainsMono)..."
curl -sSL -o "$WORK/nf.tar.xz" \
  "https://github.com/ryanoasis/nerd-fonts/releases/download/$VER/JetBrainsMono.tar.xz"
tar -xf "$WORK/nf.tar.xz" -C "$WORK"

# The Mono-patched face is the one whose icons are single-width, which is what
# keeps a Nerd Font glyph aligned in a monospace block.
SRC_REG="$WORK/JetBrainsMonoNerdFontMono-Regular.ttf"
SRC_BOLD="$WORK/JetBrainsMonoNerdFontMono-Bold.ttf"
[ -f "$SRC_REG" ] && [ -f "$SRC_BOLD" ] || { echo "unexpected archive layout" >&2; exit 1; }

# Derive the two codepoint sets FROM THE FONT, rather than hard-coding ranges:
# upstream decides which icons exist, and a hard-coded list would silently drop
# any added later.
python3 - "$SRC_REG" "$WORK/text.uni" "$WORK/icons.uni" <<'PY'
import sys
from fontTools.ttLib import TTFont

font = TTFont(sys.argv[1])
codepoints = sorted(font.getBestCmap())

def is_pua(cp):
    return (0xE000 <= cp <= 0xF8FF or 0xF0000 <= cp <= 0xFFFFD
            or 0x100000 <= cp <= 0x10FFFD)

def to_ranges(cps):
    out, start, prev = [], cps[0], cps[0]
    for cp in cps[1:]:
        if cp == prev + 1:
            prev = cp
            continue
        out.append((start, prev))
        start = prev = cp
    out.append((start, prev))
    return ",".join(f"U+{a:04X}" if a == b else f"U+{a:04X}-{b:04X}" for a, b in out)

text = [cp for cp in codepoints if not is_pua(cp)]
icons = [cp for cp in codepoints if is_pua(cp)]
open(sys.argv[2], "w").write(to_ranges(text))
open(sys.argv[3], "w").write(to_ranges(icons))
print(f"  text {len(text)} glyphs, icons {len(icons)} glyphs")
PY

mkdir -p "$OUT_DIR"
subset() { # <src> <unicodes> <dest-name>
  pyftsubset "$1" --unicodes="$2" --output-file="$OUT_DIR/$3" \
    --flavor=woff2 --layout-features='*' --no-hinting
  # Size in KB via the already-required python3: `bc` is not installed everywhere
  # (it was missing on this machine, which silently printed 0,0 KB).
  python3 -c "import os,sys; print('  %-42s %8.1f KB' % (sys.argv[1], os.path.getsize(sys.argv[2])/1024))" \
    "$3" "$OUT_DIR/$3"
}

echo "Writing into $OUT_DIR:"
subset "$SRC_REG"  "$(cat "$WORK/text.uni")"  JetBrainsMonoNerdFont-Regular.woff2
subset "$SRC_BOLD" "$(cat "$WORK/text.uni")"  JetBrainsMonoNerdFont-Bold.woff2
subset "$SRC_REG"  "$(cat "$WORK/icons.uni")" JetBrainsMonoNerdFont-Icons.woff2

echo
echo "Done. Update index.css @font-face if the family name changed."
