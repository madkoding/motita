#!/usr/bin/env python3
"""Finds the translatable units of site/index.html.

A unit is the topmost element that holds text of its own and whose descendants are
all inline (a, b, strong, em, code, span ...). site/index.html carries the Spanish
of each unit as a data-es attribute, and site/lang.js swaps it in; this module is
the single definition of "unit" shared by the check and by whoever adds one.

    python3 scripts/index_units.py            list units still missing data-es
    python3 scripts/index_units.py --check    same, and exit 1 if there are any
    python3 scripts/index_units.py --all      list every unit
"""
import html
import re
import sys
from html.parser import HTMLParser

INLINE = {"a", "b", "strong", "em", "i", "code", "kbd", "span", "small", "sup", "sub",
          "br", "mark", "abbr", "time", "button", "svg", "path", "circle", "rect", "g",
          "line", "polyline", "polygon", "ellipse", "use", "defs", "title"}
VOID = {"br", "img", "meta", "link", "input", "hr", "source", "wbr"}
SKIP = {"script", "style", "svg", "noscript", "head", "pre"}
# <pre> blocks are code; only their "# comment" lines are prose, and those are
# translated through the lines' own spans (see PRE_COMMENT below).


class Node:
    def __init__(self, tag, attrs, start, parent):
        self.tag, self.attrs, self.start, self.parent = tag, dict(attrs), start, parent
        self.children = []
        self.text = ""  # direct text, concatenated


class Tree(HTMLParser):
    def __init__(self, src):
        super().__init__(convert_charrefs=True)
        self.src = src
        self.lines = [0]
        for m in re.finditer("\n", src):
            self.lines.append(m.end())
        self.root = Node("#root", [], 0, None)
        self.cur = self.root

    def off(self):
        line, col = self.getpos()
        return self.lines[line - 1] + col

    def handle_starttag(self, tag, attrs):
        n = Node(tag, attrs, self.off(), self.cur)
        n.tag_end = n.start + len(self.get_starttag_text())
        self.cur.children.append(n)
        if tag not in VOID:
            self.cur = n

    def handle_startendtag(self, tag, attrs):
        n = Node(tag, attrs, self.off(), self.cur)
        n.tag_end = n.start + len(self.get_starttag_text())
        self.cur.children.append(n)

    def handle_endtag(self, tag):
        n = self.cur
        while n is not self.root and n.tag != tag:
            n = n.parent
        if n is not self.root:
            n.inner_end = self.off()
            self.cur = n.parent

    def handle_data(self, d):
        self.cur.text += d


def all_inline(n):
    return all(c.tag in INLINE and all_inline(c) for c in n.children)


def has_text(s):
    return re.search(r"[A-Za-z]{2}", s) is not None


def own_text(n):
    return has_text(n.text)


def deep_text(n):
    return own_text(n) or any(deep_text(c) for c in n.children if c.tag not in SKIP)


def units(n, out):
    if n.tag in SKIP and n.tag != "head":
        return
    if n.tag == "head":
        return
    if n.attrs.get("translate") == "no" or n.tag in ("code", "kbd"):
        return
    if n.tag != "#root" and (own_text(n) or n.tag in ("h1", "h2", "h3")) and all_inline(n) and deep_text(n):
        out.append(n)
        return
    for c in n.children:
        units(c, out)


def inner(src, n):
    return re.sub(r"\s+", " ", src[n.tag_end:n.inner_end]).strip()


def load(path="site/index.html"):
    src = open(path, encoding="utf-8").read()
    t = Tree(src)
    t.feed(src)
    out = []
    units(t.root, out)
    return src, out


if __name__ == "__main__":
    src, us = load()
    show_all = "--all" in sys.argv
    missing = [u for u in us if "data-es" not in u.attrs]
    for u in us if show_all else missing:
        cls = u.attrs.get("class", "")
        text = re.sub(r"\s+", " ", html.unescape(u.text)).strip()[:80]
        print(f"{u.tag}.{cls}\t{text}")
    if "--check" in sys.argv:
        if missing:
            print(f"\n{len(missing)} units of site/index.html have no data-es", file=sys.stderr)
            sys.exit(1)
        print(f"every one of the {len(us)} text units of site/index.html has a Spanish version")
