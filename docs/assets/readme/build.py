#!/usr/bin/env python3
"""Builds the README artwork (hero.svg, loop.svg, stats.svg) from the site's design system.

The colours, fonts and cut corners are the ones site/index.html declares in :root, so the
README and the landing page read as one product. Fonts are subset to the glyphs each image
uses and embedded as woff2, so the images render the same on GitHub as in a browser.

    python3 -m pip install fonttools brotli
    python3 docs/assets/readme/build.py
"""
import base64
import io
import os

from fontTools import subset
from fontTools.ttLib import TTFont

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, "..", "..", ".."))
FONTS = os.path.join(ROOT, "site", "fonts")

# Tokens copied from site/index.html (:root).
BG = "#0a0a0f"
CARD = "#16161e"
FG = "#e8e8ea"
MUTED = "#9a9aaa"
FAINT = "#7a7a8c"
ACCENT = "#4cc2ff"
CYAN = "#00f0ff"
MAGENTA = "#ff2bd6"
YELLOW = "#fcee0a"
AMBER = "#f0a040"
ROSE = "#ff6b6b"
GREEN = "#5ee08a"
BORDER = "rgba(255,255,255,0.10)"

# The font files under site/fonts the images embed.
MONO_BOLD = "JetBrainsMonoNerdFont-Bold.woff2"
MONO_REGULAR = "JetBrainsMonoNerdFont-Regular.woff2"
SANS = "Sansation-Regular.woff2"


def font_face(family: str, file: str, text: str) -> str:
    """Returns an @font-face rule with `file` subset to the characters in `text`."""
    font = TTFont(os.path.join(FONTS, file))
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = ["kern", "liga"]
    sub = subset.Subsetter(opts)
    sub.populate(text="".join(sorted(set(text + " "))))
    sub.subset(font)
    buf = io.BytesIO()
    font.flavor = "woff2"
    font.save(buf)
    data = base64.b64encode(buf.getvalue()).decode()
    return f"@font-face{{font-family:'{family}';src:url(data:font/woff2;base64,{data}) format('woff2');}}"


def cut(x: float, y: float, w: float, h: float, c: float = 10) -> str:
    """A rectangle with its top-left and bottom-right corners cut, as the site's cards."""
    return (f"M{x + c},{y} H{x + w} V{y + h - c} L{x + w - c},{y + h} H{x} V{y + c} Z")


LOGO = (
    '<g stroke="{c}" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">'
    '<path d="M8 24 Q4 18 6 12 L4 6 L12 11 Q16 8 20 11 L28 6 L26 12 Q28 18 24 24 Z"/>'
    '<circle cx="13" cy="16" r="1.5" fill="{c}" stroke="none"/>'
    '<circle cx="21" cy="16" r="1.5" fill="{c}" stroke="none"/>'
    '<path d="M16 20 L18 22 L20 20"/><path d="M24 24 Q28 22 30 18"/></g>'
)


def grid(step: int = 32) -> str:
    """The faint background grid, as an SVG <pattern> with id "grid"."""
    return (f'<pattern id="grid" width="{step}" height="{step}" patternUnits="userSpaceOnUse">'
            f'<path d="M{step} 0 H0 V{step}" fill="none" stroke="rgba(255,255,255,0.035)"/></pattern>')


def svg_open(w: int, h: int, label: str) -> str:
    """The root <svg> start tag, sized w x h and labelled for screen readers."""
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" '
            f'role="img" aria-label="{label}">')


def glow(gid: str, cx: str, cy: str, r: str, colour: str, opacity: str) -> str:
    """A radial gradient from colour at opacity in the centre to transparent at the edge."""
    return (f'<radialGradient id="{gid}" cx="{cx}" cy="{cy}" r="{r}">'
            f'<stop offset="0" stop-color="{colour}" stop-opacity="{opacity}"/>'
            f'<stop offset="1" stop-color="{colour}" stop-opacity="0"/></radialGradient>')


def arrow_marker(mid: str, colour: str) -> str:
    """A filled triangular arrowhead marker with id mid."""
    return (f'<marker id="{mid}" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" '
            f'orient="auto"><path d="M0,0 L10,5 L0,10 Z" fill="{colour}"/></marker>')


def write(name: str, svg: str) -> None:
    """Write svg next to this script as name and print its size."""
    path = os.path.join(HERE, name)
    with open(path, "w", encoding="utf-8") as f:
        f.write(svg)
    print(f"{name}: {os.path.getsize(path) // 1024} KiB")


def hero() -> None:
    """hero.svg: the pitch on the left, a terminal run that retries until it passes on the right."""
    W, H = 1280, 420
    pill = "FREE & OPEN SOURCE · ZERO DEPENDENCIES"
    title1, title2a, title2b = "The AI agent that", "proves", " it's done."
    lede1 = "Writes the change, runs your real tests, reads the failures"
    lede2 = "and fixes them. It only says done when the check passes."
    term = [
        ("$", ACCENT, 'motita "add a JSON export"', FG),
        ("✕", ROSE, "attempt 1  go test: undefined: json", MUTED),
        ("↻", AMBER, "retry with the real error", MUTED),
        ("✓", GREEN, "attempt 2  go test ./... ok", MUTED),
        ("◆", CYAN, "DONE  3/3 checks · 2 files", FG),
    ]
    mono_text = (pill + "Motita" + title1 + title2a + title2b + "127.0.0.1:7477"
                 + "".join(a + b for a, _, b, _ in term) + "▍")
    css = (font_face("JBM", MONO_BOLD, pill + "Motita" + title1 + title2a + title2b + "DONE")
           + font_face("JBMR", MONO_REGULAR, mono_text)
           + font_face("Sansation", SANS, lede1 + lede2))
    css += (
        ".m{font-family:'JBM',ui-monospace,monospace;font-weight:700}"
        ".r{font-family:'JBMR',ui-monospace,monospace;white-space:pre}"
        ".s{font-family:'Sansation',system-ui,sans-serif}"
        "@keyframes in{from{opacity:0;transform:translateY(6px)}}"
        "@keyframes blink{50%{opacity:0}}"
        "@keyframes draw{from{transform:scaleX(0)}}"
        ".l{animation:in .6s cubic-bezier(.16,1,.3,1) backwards}"
        ".u{transform-box:fill-box;transform-origin:left;animation:draw .8s 1s cubic-bezier(.16,1,.3,1) backwards}"
        ".c{animation:blink 1.1s steps(1) infinite}"
    )
    tx, ty, tw, th = 700, 92, 520, 250
    lines = []
    for i, (glyph, gc, text, tc) in enumerate(term):
        y = ty + 82 + i * 34
        weight = ' class="m"' if glyph == "◆" else ' class="r"'
        escaped = text.replace("&", "&amp;").replace(chr(34), "&quot;")
        lines.append(
            f'<g class="l" style="animation-delay:{0.5 + i * 0.45:.2f}s">'
            f'<text x="{tx + 28}" y="{y}" class="r" font-size="17" fill="{gc}">{glyph}</text>'
            f'<text x="{tx + 54}" y="{y}"{weight} font-size="17" fill="{tc}">{escaped}</text></g>')
    cursor_y = ty + 82 + len(term) * 34
    title = "Motita: the AI agent that proves it's done"
    neon = ('<filter id="neon" x="-20%" y="-50%" width="140%" height="200%">'
            '<feGaussianBlur stdDeviation="6" result="b"/>'
            '<feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>')
    pill_text = (f'<text x="94" y="84.5" class="m" font-size="14" letter-spacing="1.4" fill="{ACCENT}">'
                 f'{pill.replace("&", "&amp;")}</text>')
    headline1 = (f'<text x="64" y="218" class="m" font-size="50" letter-spacing="-1.2" fill="{FG}">'
                 f'{title1}</text>')
    headline2 = (f'<text x="64" y="280" class="m" font-size="50" letter-spacing="-1.2" fill="{FG}">'
                 f'<tspan fill="{MAGENTA}" filter="url(#neon)">{title2a}</tspan>{title2b}</text>')
    dot = 'r="4.5" fill="rgba(255,255,255,.14)"/>'
    dots = (f'<circle cx="{tx + 26}" cy="{ty + 17}" {dot}<circle cx="{tx + 42}" cy="{ty + 17}" {dot}'
            f'<circle cx="{tx + 58}" cy="{ty + 17}" {dot}')
    svg = f'''{svg_open(W, H, title)}
<title>{title}</title>
<defs><style>{css}</style>{grid()}
{glow("glow", "18%", "30%", "60%", ACCENT, ".16")}
{glow("glow2", "85%", "80%", "45%", MAGENTA, ".10")}
{neon}
<clipPath id="frame"><path d="{cut(0, 0, W, H, 22)}"/></clipPath></defs>
<g clip-path="url(#frame)">
<rect width="{W}" height="{H}" fill="{BG}"/><rect width="{W}" height="{H}" fill="url(#grid)"/>
<rect width="{W}" height="{H}" fill="url(#glow)"/><rect width="{W}" height="{H}" fill="url(#glow2)"/>
</g>
<path d="{cut(1, 1, W - 2, H - 2, 22)}" fill="none" stroke="{CYAN}" stroke-opacity=".45"/>
<g class="l"><rect x="64" y="64" width="{len(pill) * 9.8 + 44:.0f}" height="30" fill="{ACCENT}" fill-opacity=".2"/>
<circle cx="80" cy="79" r="3.5" fill="{ACCENT}"/>
{pill_text}</g>
<g class="l" style="animation-delay:.1s"><g transform="translate(64,120) scale(1.15)">{LOGO.format(c=ACCENT)}</g>
<text x="112" y="148" class="m" font-size="28" fill="{ACCENT}">Motita</text></g>
<g class="l" style="animation-delay:.25s">{headline1}</g>
<g class="l" style="animation-delay:.45s">{headline2}
<rect class="u" x="64" y="291" width="177" height="5" fill="{MAGENTA}" filter="url(#neon)"/></g>
<g class="l" style="animation-delay:.6s"><text x="64" y="336" class="s" font-size="19" fill="{MUTED}">{lede1}</text>
<text x="64" y="362" class="s" font-size="19" fill="{MUTED}">{lede2}</text></g>
<g class="l" style="animation-delay:.3s">
<path d="{cut(tx, ty, tw, th, 12)}" fill="{CARD}" stroke="{CYAN}" stroke-opacity=".35"/>
<path d="M{tx + 12},{ty + 34} H{tx + tw}" stroke="{BORDER}"/>
{dots}
<text x="{tx + 82}" y="{ty + 22}" class="r" font-size="13" fill="{FAINT}">127.0.0.1:7477</text></g>
{"".join(lines)}
<text x="{tx + 28}" y="{cursor_y}" class="r c" font-size="17" fill="{ACCENT}">▍</text>
</svg>
'''
    write("hero.svg", svg)


def loop() -> None:
    """loop.svg: task, model, sandbox and anchor as cards, with the FAIL loop back and the escalation."""
    W, H = 1280, 360
    cards = [
        # x, label, title, line1, line2, edge
        (40, "TASK", "You ask", "in plain words, plus", "the check that proves it", FAINT),
        (300, "B · MODEL", "Proposes", "an action. It never gets", "to declare success.", AMBER),
        (560, "C · SANDBOX", "Runs it", "under real limits: memory,", "CPU, time, no network.", ACCENT),
        (820, "A · ANCHOR", "Your code decides", "exit code, output, your", "own tests. No opinions.", CYAN),
    ]
    end = (1080, "PASS", "Ships", "commit · push", "publish · notify", GREEN)
    fail_text = "FAIL · the real error goes back to the model"
    esc_text = "out of retries → escalate, never a fake PASS"
    every = "".join(c[1] + c[2] for c in cards + [end]) + fail_text + esc_text
    body = "".join(c[3] + c[4] for c in cards + [end])
    css = (font_face("JBM", MONO_BOLD, every)
           + font_face("Sansation", SANS, body)
           + ".m{font-family:'JBM',ui-monospace,monospace;font-weight:700}"
           + ".s{font-family:'Sansation',system-ui,sans-serif}"
           + "@keyframes flow{to{stroke-dashoffset:-24}}"
           + ".f{stroke-dasharray:6 6;animation:flow 1s linear infinite}")
    y, h = 110, 140
    parts = []
    for x, label, title, l1, l2, edge in cards + [end]:
        w = 220 if x < 1080 else 160
        parts.append(
            f'<path d="{cut(x, y, w, h, 10)}" fill="{CARD}" stroke="{edge}" '
            f'stroke-opacity="{.9 if edge == CYAN else .5}"/>'
            f'<rect x="{x}" y="{y + h - 3}" width="{w - 10}" height="3" fill="{edge}" fill-opacity=".8"/>'
            f'<text x="{x + 18}" y="{y + 30}" class="m" font-size="12" letter-spacing="1.3" '
            f'fill="{edge}">{label}</text>'
            f'<text x="{x + 18}" y="{y + 62}" class="m" font-size="18" fill="{FG}">{title}</text>'
            f'<text x="{x + 18}" y="{y + 92}" class="s" font-size="14.5" fill="{MUTED}">{l1}</text>'
            f'<text x="{x + 18}" y="{y + 113}" class="s" font-size="14.5" fill="{MUTED}">{l2}</text>')
    arrows = ""
    for x in (260, 520, 780, 1040):
        arrows += (f'<path d="M{x + 2},{y + h / 2} H{x + 36}" stroke="{ACCENT}" stroke-width="2" class="f" '
                   f'marker-end="url(#ar)"/>')
    # FAIL: from the anchor back to the model, over the top.
    fail = (f'<path d="M930,{y - 4} V58 H410 V{y - 8}" fill="none" stroke="{ROSE}" stroke-width="2" class="f" '
            f'marker-end="url(#ar-rose)"/>'
            f'<rect x="480" y="44" width="380" height="28" fill="{BG}"/>'
            f'<text x="670" y="63" class="m" font-size="13" text-anchor="middle" fill="{ROSE}">{fail_text}</text>')
    esc = (f'<path d="M930,{y + h + 6} V{y + h + 52} H700" fill="none" stroke="{MAGENTA}" stroke-opacity=".7" '
           f'stroke-width="2" stroke-dasharray="3 5"/>'
           f'<text x="690" y="{y + h + 57}" class="m" font-size="13" text-anchor="end" fill="{MAGENTA}">'
           f'{esc_text}</text>')
    title = "How motita works: the model proposes, the sandbox runs, your check decides"
    background = f'<rect width="{W}" height="{H}" fill="{BG}"/><rect width="{W}" height="{H}" fill="url(#grid)"/>'
    svg = f'''{svg_open(W, H, title)}
<title>{title}</title>
<defs><style>{css}</style>{grid()}
{arrow_marker("ar", ACCENT)}
{arrow_marker("ar-rose", ROSE)}
<clipPath id="frame"><path d="{cut(0, 0, W, H, 18)}"/></clipPath></defs>
<g clip-path="url(#frame)">{background}</g>
<path d="{cut(1, 1, W - 2, H - 2, 18)}" fill="none" stroke="{CYAN}" stroke-opacity=".3"/>
{fail}{"".join(parts)}{arrows}{esc}
</svg>
'''
    write("loop.svg", svg)


def stats() -> None:
    """stats.svg: the five headline numbers, one cut-corner card each."""
    W, H = 1280, 150
    items = [("0", "dependencies", ACCENT), ("1", "static binary", ACCENT), ("10", "platforms", ACCENT),
             ("100%", "test coverage", GREEN), ("484 MB", "of RAM is enough", MAGENTA)]
    css = (font_face("JBM", MONO_BOLD, "".join(v for v, _, _ in items))
           + font_face("Sansation", SANS, "".join(l for _, l, _ in items))
           + ".m{font-family:'JBM',ui-monospace,monospace;font-weight:700}"
           + ".s{font-family:'Sansation',system-ui,sans-serif}")
    cw = (W - 80 - 4 * 16) / 5
    parts = []
    for i, (v, label, c) in enumerate(items):
        x = 40 + i * (cw + 16)
        cx = x + cw / 2
        parts.append(
            f'<path d="{cut(x, 24, cw, 102, 10)}" fill="{CARD}" stroke="{BORDER}"/>'
            f'<rect x="{x + 10}" y="24" width="{cw - 10}" height="2" fill="{c}" fill-opacity=".8"/>'
            f'<text x="{cx}" y="80" class="m" font-size="36" text-anchor="middle" fill="{c}">{v}</text>'
            f'<text x="{cx}" y="108" class="s" font-size="15" text-anchor="middle" fill="{MUTED}">{label}</text>')
    label = "0 dependencies, 1 static binary, 10 platforms, 100% test coverage, runs in 484 MB of RAM"
    title = "0 dependencies · 1 static binary · 10 platforms · 100% test coverage · 484 MB of RAM is enough"
    svg = f'''{svg_open(W, H, label)}
<title>{title}</title>
<defs><style>{css}</style><clipPath id="frame"><path d="{cut(0, 0, W, H, 16)}"/></clipPath></defs>
<g clip-path="url(#frame)"><rect width="{W}" height="{H}" fill="{BG}"/></g>
{"".join(parts)}
</svg>
'''
    write("stats.svg", svg)


if __name__ == "__main__":
    hero()
    loop()
    stats()
