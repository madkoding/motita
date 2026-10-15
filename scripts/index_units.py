#!/usr/bin/env python3
"""Finds the translatable units of site/index.html.

A unit is the topmost element that holds text of its own and whose descendants are
all inline (a, b, strong, em, code, span ...). site/index.html carries the Spanish
of each unit as a data-es attribute, and site/lang.js swaps it in; this module is
the single definition of "unit" shared by the check and by whoever adds one.

    python3 scripts/index_units.py            list units still missing data-es
    python3 scripts/index_units.py --check    same, and exit 1 if there are any
    python3 scripts/index_units.py --all      list every unit

An element that must stay as it is (a product name, a code sample) says so with
translate="no" and is not a unit.
"""
from __future__ import annotations

import html
import re
import sys
from html.parser import HTMLParser

INLINE = {"a", "b", "strong", "em", "i", "code", "kbd", "span", "small", "sup", "sub",
          "br", "mark", "abbr", "time", "button", "svg", "path", "circle", "rect", "g",
          "line", "polyline", "polygon", "ellipse", "use", "defs", "title"}
VOID = {"br", "img", "meta", "link", "input", "hr", "source", "wbr"}
# <pre> blocks are code, and the head holds no visible prose of the page body.
SKIP = {"script", "style", "svg", "noscript", "head", "pre"}
HEADINGS = ("h1", "h2", "h3")


class Node:
    """One element: its tag, attributes, children and the text it holds directly."""

    def __init__(self, tag: str, attrs: list[tuple[str, str | None]], start: int, parent: Node | None) -> None:
        self.tag = tag
        self.attrs = dict(attrs)
        self.start = start
        self.parent = parent
        self.children: list[Node] = []
        self.text = ""
        self.tag_end = start
        self.inner_end = start


class Tree(HTMLParser):
    """Builds a tree that remembers where each element sits in the source text."""

    def __init__(self, src: str) -> None:
        super().__init__(convert_charrefs=True)
        self.lines = [0]
        for m in re.finditer("\n", src):
            self.lines.append(m.end())
        self.root = Node("#root", [], 0, None)
        self.cur = self.root

    def off(self) -> int:
        """Offset in the source of the construct being parsed."""
        line, col = self.getpos()
        return self.lines[line - 1] + col

    def _add(self, tag: str, attrs: list[tuple[str, str | None]]) -> Node:
        n = Node(tag, attrs, self.off(), self.cur)
        n.tag_end = n.start + len(self.get_starttag_text() or "")
        self.cur.children.append(n)
        return n

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        n = self._add(tag, attrs)
        if tag not in VOID:
            self.cur = n

    def handle_startendtag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        self._add(tag, attrs)

    def handle_endtag(self, tag: str) -> None:
        n = self.cur
        while n is not self.root and n.tag != tag:
            n = n.parent
        if n is not self.root:
            n.inner_end = self.off()
            self.cur = n.parent

    def handle_data(self, data: str) -> None:
        self.cur.text += data


def all_inline(n: Node) -> bool:
    """True when every descendant is an inline element."""
    return all(c.tag in INLINE and all_inline(c) for c in n.children)


def has_text(s: str) -> bool:
    """True when the string holds at least two letters."""
    return re.search(r"[A-Za-z]{2}", s) is not None


def deep_text(n: Node) -> bool:
    """True when the element or anything inside it (outside <svg> etc.) holds text."""
    if has_text(n.text):
        return True
    return any(deep_text(c) for c in n.children if c.tag not in SKIP)


def is_unit(n: Node) -> bool:
    """Whether n is a translatable unit (see the module docstring)."""
    if n.tag == "#root" or n.attrs.get("translate") == "no" or n.tag in ("code", "kbd"):
        return False
    has_own = has_text(n.text) or n.tag in HEADINGS
    return has_own and all_inline(n) and deep_text(n)


def units(n: Node, out: list[Node]) -> None:
    """Collects the topmost units under n, in document order."""
    if n.tag in SKIP or n.attrs.get("translate") == "no":
        return
    if is_unit(n):
        out.append(n)
        return
    for c in n.children:
        units(c, out)


def inner(src: str, n: Node) -> str:
    """The element's inner HTML with whitespace collapsed."""
    return re.sub(r"\s+", " ", src[n.tag_end:n.inner_end]).strip()


def load(path: str = "site/index.html") -> tuple[str, list[Node]]:
    """Reads the page and returns its source and its units."""
    with open(path, encoding="utf-8") as f:
        src = f.read()
    tree = Tree(src)
    tree.feed(src)
    out: list[Node] = []
    units(tree.root, out)
    return src, out


def main(argv: list[str]) -> int:
    """Command-line entry point; returns the exit status."""
    _, us = load()
    missing = [u for u in us if "data-es" not in u.attrs]
    shown = us if "--all" in argv else missing
    for u in shown:
        text = re.sub(r"\s+", " ", html.unescape(u.text)).strip()[:80]
        print(f"{u.tag}.{u.attrs.get('class', '')}\t{text}")
    if "--check" not in argv:
        return 0
    if missing:
        print(f"\n{len(missing)} units of site/index.html have no data-es", file=sys.stderr)
        return 1
    print(f"every one of the {len(us)} text units of site/index.html has a Spanish version")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
