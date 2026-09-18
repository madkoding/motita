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

BG = (18, 18, 24)
FG = (229, 229, 229)
FONT_SIZE = 16
PADDING = 16
LINE_HEIGHT = 20

ESC = re.compile(r'\x1b\[(\d+(?:;\d+)*)m')

def parse_ansi(text):
    """Yield (char, fg, bg) tuples."""
    fg, bg = FG, None
    pos = 0
    for m in ESC.finditer(text):
        start, end = m.span()
        if start > pos:
            for ch in text[pos:start]:
                yield ch, fg, bg
        codes = [int(c) for c in m.group(1).split(';') if c]
        i = 0
        while i < len(codes):
            c = codes[i]
            if c == 0:
                fg, bg = FG, None
            elif 30 <= c <= 37:
                fg = PALETTE[c - 30]
            elif 40 <= c <= 47:
                bg = PALETTE[c - 40]
            i += 1
        pos = end
    for ch in text[pos:]:
        yield ch, fg, bg

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
