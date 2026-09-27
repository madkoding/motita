#!/usr/bin/env python3
"""Turn the FluentUI emoji SVGs into one WebP per emoji, named after its codepoint.

Why this shape:

* WebP at an icon size, not the source SVG. The sources are 32x32 vector and the COLOR
  style weighs 23 MB; `go:embed` does not compress, so embedding the SVGs raw would take
  the binary from 15.6 MiB to ~37 MiB against a 20 MiB ceiling. WebP is already
  compressed, so what it weighs is what it costs.
* Named by CODEPOINT (`1f600.webp`). The browser can compute that name from the character
  itself, which means no 1000-entry lookup table has to ship in the bundle: the renderer
  only needs the SET of codepoints that exist, so a character with no FluentUI asset keeps
  rendering as the font's own emoji instead of a broken image.
* ffmpeg does the work because it is already here and its librsvg decoder handles these
  files; there is no rsvg-convert, cairosvg, Pillow or ImageMagick on this machine.

Source: https://github.com/microsoft/fluentui-emoji (MIT).
"""
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

SRC = Path(sys.argv[1] if len(sys.argv) > 1 else "/tmp/mdm/fu/assets")
OUT = Path(sys.argv[2] if len(sys.argv) > 2 else "web/public/emoji")
STYLE = sys.argv[3] if len(sys.argv) > 3 else "Color"
SIZE = int(sys.argv[4] if len(sys.argv) > 4 else 64)
QUALITY = int(sys.argv[5] if len(sys.argv) > 5 else 85)


def codepoint(meta: dict) -> str | None:
    """The emoji's filename, derived from its own codepoint(s).

    FluentUI records `unicode` as lowercase hex, and the separator is a SPACE, not a dash:
    measured, 547 of 1595 assets read `1f170 fe0f` rather than `1f170`. Getting that wrong
    is silent — the parse fails, the emoji is skipped, and the set is merely smaller — which
    is exactly how it read as 1048 assets the first time.

    Trailing `fe0f` (VARIATION SELECTOR-16) is DROPPED. A character keyboard-inserted from
    a picker usually carries it, a character typed by hand usually does not, and both name
    the same glyph: keeping it would give the same emoji two filenames and let half the
    real-world input miss. Nothing here is a ZWJ sequence (measured: 0 of 1595), so the
    remaining parts are joined as-is for any future multi-codepoint asset.
    """
    u = (meta.get("unicode") or "").strip().lower()
    if not u:
        return None
    parts = [p for p in re.split(r"[\s-]+", u) if re.fullmatch(r"[0-9a-f]+", p)]
    # fe0f only ever modifies the character before it; on its own it names nothing.
    parts = [p for p in parts if p != "fe0f"]
    if not parts:
        return None
    return "-".join(format(int(p, 16), "x") for p in parts)


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    emoji = {}

    for d in sorted(os.listdir(SRC)):
        base = SRC / d
        if not base.is_dir():
            continue
        meta_path = base / "metadata.json"
        if not meta_path.exists():
            continue
        meta = json.loads(meta_path.read_text(encoding="utf8"))
        svg = base / STYLE / f"{d.lower().replace(' ', '_')}_{STYLE.lower()}.svg"
        if not svg.exists():
            # A handful of assets use the display name rather than the cldr name.
            cands = list((base / STYLE).glob("*.svg")) if (base / STYLE).is_dir() else []
            if not cands:
                continue
            svg = cands[0]
        cp = codepoint(meta)
        if not cp:
            continue
        emoji[cp] = {"glyph": meta.get("glyph", ""), "name": meta.get("cldr", d),
                     "group": meta.get("group", ""), "src": str(svg)}

    print(f"emoji with a {STYLE} asset: {len(emoji)}")

    ok = 0
    failed = []
    total = 0
    for cp, info in sorted(emoji.items()):
        dst = OUT / f"{cp}.webp"
        r = subprocess.run(
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", info["src"],
             "-vf", f"scale={SIZE}:{SIZE}:flags=lanczos", "-c:v", "libwebp",
             "-quality", str(QUALITY), "-compression_level", "6", str(dst)],
            capture_output=True)
        if r.returncode != 0 or not dst.exists() or dst.stat().st_size == 0:
            failed.append((cp, r.stderr.decode()[:160]))
            dst.unlink(missing_ok=True)
            continue
        ok += 1
        total += dst.stat().st_size

    print(f"wrote {ok} webp to {OUT} ({total/1e6:.2f} MB, {total//max(ok,1)} B each)")
    if failed:
        print(f"FAILED ({len(failed)}):")
        for cp, err in failed[:10]:
            print(f"  {cp}: {err}")

    manifest = OUT / "codepoints.json"
    have = sorted(cp for cp in emoji if (OUT / f"{cp}.webp").exists())
    manifest.write_text(json.dumps(have, separators=(",", ":")))
    print(f"manifest: {manifest} ({manifest.stat().st_size} B)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
