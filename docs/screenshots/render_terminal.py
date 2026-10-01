#!/usr/bin/env python3
"""Render captured terminal output (ANSI colors) to a PNG screenshot.

Usage: render_terminal.py <input.txt> <output.png> [--title TITLE]
"""
import sys
from PIL import Image, ImageDraw, ImageFont
import re

# Terminal palette (ANSI 30-37 foreground, 40-47 background)
PALETTE = {
    0: (0, 0, 0),       # black
    1: (205, 49, 49),   # red
    2: (13, 188, 121),  # green
    3: (229, 229, 16),  # yellow
    4: (36, 114, 200),  # blue
    5: (188, 63, 188),  # magenta
    6: (17, 168, 205),  # cyan
    7: (229, 229, 229), # white/light gray
}

# The bright half of the palette (ANSI 90-97 foreground, 100-107 background).
BRIGHT = {
    0: (118, 118, 118), # bright black: the muted text
    1: (241, 76, 76),
    2: (35, 209, 139),
    3: (245, 245, 67),
    4: (59, 142, 234),
    5: (214, 112, 214),
    6: (41, 184, 219),
    7: (255, 255, 255),
}

BG = (18, 18, 24)
FG = (229, 229, 229)
FONT_SIZE = 16
PADDING = 16
LINE_HEIGHT = 20

ESC = re.compile(r'\x1b\[(\d+(?:;\d+)*)m')

def _dim(c):
    return tuple(int(v * 0.6 + b * 0.4) for v, b in zip(c, BG))

def _xterm256(n):
    """The colour of an xterm 256-colour index, which is what tmux writes for some colours."""
    if n < 8:
        return PALETTE[n]
    if n < 16:
        return BRIGHT[n - 8]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return (steps[n // 36], steps[(n // 6) % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return (v, v, v)

def parse_ansi(text):
    """Yield (char, fg, bg) tuples."""
    fg, bg, dim = FG, None, False
    pos = 0
    for m in ESC.finditer(text):
        start, end = m.span()
        if start > pos:
            for ch in text[pos:start]:
                yield ch, (_dim(fg) if dim else fg), bg
        codes = [int(c) for c in m.group(1).split(';') if c]
        i = 0
        while i < len(codes):
            c = codes[i]
            if c == 0:
                fg, bg, dim = FG, None, False
            elif c == 2:
                dim = True
            elif c == 22:
                dim = False
            elif c == 39:
                fg = FG
            elif c == 49:
                bg = None
            elif 30 <= c <= 37:
                fg = PALETTE[c - 30]
            elif 40 <= c <= 47:
                bg = PALETTE[c - 40]
            elif 90 <= c <= 97:
                fg = BRIGHT[c - 90]
            elif 100 <= c <= 107:
                bg = BRIGHT[c - 100]
            elif c in (38, 48) and i + 2 < len(codes) and codes[i + 1] == 5:
                col = _xterm256(codes[i + 2])
                if c == 38:
                    fg = col
                else:
                    bg = col
                i += 2
            i += 1
        pos = end
    for ch in text[pos:]:
        yield ch, (_dim(fg) if dim else fg), bg

def render(text, out_path, title=None):
    lines = text.splitlines()
    # Use default monospace font
    try:
        font = ImageFont.truetype("DejaVuSansMono.ttf", FONT_SIZE)
    except Exception:
        font = ImageFont.load_default()
    # measure
    max_w = 0
    for line in lines:
        clean = ESC.sub('', line)
        bbox = font.getbbox(clean)
        w = bbox[2] - bbox[0] if bbox else 0
        max_w = max(max_w, w)
    title_h = 0
    if title:
        bbox = font.getbbox(title)
        title_h = (bbox[3]-bbox[1]) + 12
    img_w = max(max_w + 2*PADDING, 600)
    img_h = max(len(lines)*LINE_HEIGHT + 2*PADDING + title_h, 200)
    img = Image.new('RGB', (img_w, img_h), BG)
    draw = ImageDraw.Draw(img)
    if title:
        draw.text((PADDING, PADDING-2), title, fill=(150,150,160), font=font)
    y = PADDING + title_h
    for line in lines:
        x = PADDING
        for ch, fg_color, bg_color in parse_ansi(line):
            if ch == '\t':
                x += font.getlength(' ')*4
                continue
            cw = font.getlength(ch)
            if bg_color:
                draw.rectangle([x, y, x+cw, y+LINE_HEIGHT], fill=bg_color)
            draw.text((x, y), ch, fill=fg_color, font=font)
            x += cw
        y += LINE_HEIGHT
    img.save(out_path)
    print(f"saved {out_path} ({img_w}x{img_h})")

if __name__ == "__main__":
    inp, outp = sys.argv[1], sys.argv[2]
    title = sys.argv[4] if len(sys.argv) > 3 and sys.argv[3] == '--title' else None
    render(open(inp, encoding='utf-8').read(), outp, title)
