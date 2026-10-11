"""The checks cdp_markdown.py runs on the rendered conversation, one function per property.

Every assertion is against a RENDERED value read back from the page (a bounding box, a computed
colour, a text node), never against a class name. Each check prints its own ok/FAIL line.
"""

from __future__ import annotations

import asyncio
import json
import pathlib
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from cdp_http import JSON
    from cdp_markdown import CDP

# What a run concluded, printed as it goes and summed up at the end.
failures: list[str] = []
notes: list[str] = []


def bad(msg: str) -> None:
    """Record and print one failed check."""
    failures.append(msg)
    print(f"  FAIL  {msg}")


def ok(msg: str) -> None:
    """Print one passed check."""
    print(f"  ok    {msg}")


def note(msg: str) -> None:
    """Record and print a fact that is not a verdict."""
    # A fact worth printing that is not a verdict: the probe must never FAIL on data
    # it was given (a shortened quiet window means a state was not sampled, not a defect).
    notes.append(msg)
    print(f"  note  {msg}")


async def save_json(path: str, data: JSON, ensure_ascii: bool = False) -> None:
    """Write data as indented JSON next to the screenshots."""
    await asyncio.to_thread(pathlib.Path(path).write_text,
                            json.dumps(data, indent=2, ensure_ascii=ensure_ascii))


def check_text(m: dict[str, Any]) -> None:
    """Hard breaks, escapes and inline HTML."""
    # --- line breaks ------------------------------------------------------
    if m["lineBreak"]["br"] >= 1 and m["lineBreak"]["paragraphHeight"] > 20:
        ok(f"two trailing spaces make a hard break "
           f"({m['lineBreak']['br']} <br>, paragraph {m['lineBreak']['paragraphHeight']}px tall)")
    else:
        bad(f"two trailing spaces did not break the line: "
            f"{m['lineBreak']['br']} <br>, paragraph {m['lineBreak']['paragraphHeight']}px")

    # --- escapes ----------------------------------------------------------
    esc = m["escape"]
    if esc["text"] == "\\esto no es cursiva\\" and esc["emInside"] == 0:
        ok("an escaped pair of asterisks stays literal, with no emphasis")
    else:
        bad(f"the escaped asterisks were not kept literal: {esc['text']!r} "
            f"with {esc['emInside']} <em> inside")

    # --- inline HTML ------------------------------------------------------
    html = m["inlineHtml"]
    if html["kbd"] == 2 and html["mark"] == 1 and html["sub"] == 1 and html["sup"] == 2:
        ok(f"inline HTML renders ({html['kbd']} kbd, {html['mark']} mark, "
           f"{html['sub']} sub, {html['sup']} sup)")
    else:
        bad(f"inline HTML did not render as elements: {html}")
    if html["markBg"] and html["markBg"] not in ("rgba(0, 0, 0, 0)", "transparent"):
        ok(f"<mark> is actually painted ({html['markBg']})")
    else:
        bad(f"<mark> has no background colour ({html['markBg']}): the tag is there "
            f"but nothing shows")


def check_blocks(m: dict[str, Any]) -> None:
    """Footnotes, the definition list and the formulas."""
    # --- footnotes --------------------------------------------------------
    fn = m["footnotes"]
    if fn["refs"] >= 1 and fn["targetExists"] and fn["listed"]:
        ok(f"a footnote is a working reference ({fn['refs']} ref, target resolves, listed)")
    else:
        bad(f"the footnote did not become a working reference: {fn}")

    # --- definition list --------------------------------------------------
    dl = m["deflist"]
    if dl["dl"] and dl["dt"] and dl["dd"]:
        ok(f"the definition list renders (dt={dl['dt']!r}, dd={dl['dd']!r})")
    else:
        bad(f"the definition list did not render as a <dl>: {dl}")

    # --- math -------------------------------------------------------------
    math = m["math"]
    if math["count"] >= 3 and math["blockVisible"]:
        ok(f"the formulas are typeset as MathML ({math['count']} <math>, "
           f"block height > 0): {math['geom']}")
    else:
        bad(f"the formulas were not typeset: {math}")
    if math["rawTexShowing"] or math["annotationLeak"]:
        bad(f"the RAW LaTeX is still visible ({math['rawTexShowing']} / "
            f"annotation text inside <math>: {math['annotationLeak']}) — the "
            f"accessible annotation was dropped but its text was left behind")
    else:
        ok("no raw LaTeX is printed beside the typeset formula")


def check_broken_diagram(broken: list[dict[str, Any]]) -> None:
    """The one malformed diagram is reported by this renderer's error box, not by mermaid."""
    if len(broken) != 1:
        bad(f"expected 1 deliberately-broken diagram, found {len(broken)}: the failure "
            f"path is not being exercised, so this check would pass on a page that "
            f"shows mermaid's own error icon")
    else:
        b = broken[0]
        if b["error"]:
            ok(f"a malformed diagram is reported by the renderer, not by mermaid "
               f"(reason: {(b['error'] or '').split(' || ')[0][:70]!r})")
        else:
            bad(f"the malformed diagram did not produce this renderer's error box "
                f"(done={b['done']!r}): mermaid's own error icon is what a reader would "
                f"see instead of the reason and the source")


async def check_no_mermaid_icon(c: CDP) -> None:
    """Mermaid's own error icon is nowhere on the page."""
    mermaid_own = await c.js("""(() => ({
      inBody: [...document.body.querySelectorAll('svg')]
        .filter(s => /Syntax error in text/.test(s.textContent || '')).length,
      version: document.body.textContent.includes('mermaid version'),
      inBlock: [...document.querySelectorAll('.mermaid-block svg')]
        .filter(s => /Syntax error in text/.test(s.textContent || '')).length,
    }))()""")
    if mermaid_own["inBody"] or mermaid_own["version"]:
        bad(f"mermaid's own error icon is on the page ({mermaid_own}): it draws a red X "
            f"with 'Syntax error in text' and its version, and that path does not clean "
            f"up its temporary element — which is how it ends up outside the block")
    else:
        ok("no 'Syntax error in text' icon anywhere on the page, inside or outside a block")


def check_valid_diagrams(expected: list[tuple[str, Any, str]], valid: list[dict[str, Any]]) -> None:
    """Every diagram type draws its own content."""
    for (name, check, what), d in zip(expected, valid):
        if d["error"]:
            bad(f"the {name} diagram failed to render: {d['error']}")
        elif not d["svg"] or d["w"] == 0 or d["h"] == 0:
            bad(f"the {name} diagram has no drawn size ({d['w']}x{d['h']})")
        elif not check(d):
            bad(f"the {name} diagram drew no {what}: labels={d['labels']} "
                f"actors={d['actors']} tasks={d['tasks']} slices={d['slices']} "
                f"texts={d['texts'][:6]}")
        else:
            ok(f"the {name} diagram draws its own content ({what}), "
               f"{d['w']}x{d['h']}, done={d['done']}")


async def check_diagrams(c: CDP, m: dict[str, Any]) -> None:
    """Every diagram type is drawn and labelled, and the malformed one is reported, not drawn."""
    # --- diagrams: every type, drawn AND labelled -------------------------
    # The point of the full mermaid build is that ANY diagram type works, so one
    # type rendering is not evidence. Each entry names the ink that proves its own
    # type drew: a flowchart has node labels and an edge, a sequence has actors, a
    # gantt has task/section text, a pie has slices.
    diagrams = m["diagrams"]
    expected = [
        ("flowchart", lambda d: d["labels"] == ["Inicio", "Fin"] and d["edges"] >= 1,
         "node labels Inicio/Fin and an edge"),
        ("sequenceDiagram", lambda d: "Usuario" in d["actors"] and "Servidor" in d["actors"],
         "both actors"),
        ("gantt", lambda d: any("trabajo" in t for t in d["tasks"]),
         "the task text"),
        ("pie", lambda d: d["slices"] >= 2, "its slices"),
    ]
    # FOUR diagram types plus the one malformed diagram at the end of the fixture.
    if len(diagrams) != len(expected) + 1:
        bad(f"expected {len(expected)} diagrams plus the malformed one on the page, "
            f"found {len(diagrams)}")
    # The malformed diagram is the one that proves the failure path: it must be reported
    # by THIS renderer (a .render-error box with the reason and the offending source), not
    # by mermaid drawing its own "Syntax error in text" icon. Suppressing that icon is why
    # it is `suppressErrorRendering: true` in mermaid.ts.
    valid, broken = diagrams[:len(expected)], diagrams[len(expected):]
    check_broken_diagram(broken)
    await check_no_mermaid_icon(c)
    check_valid_diagrams(expected, valid)

    # The exact regression this file exists for: empty boxes with a live arrow.
    mm = m["mermaid"]
    if not (mm["present"] and mm["svg"]):
        bad("the first diagram was not drawn at all")
    elif not diagrams or diagrams[0]["labels"] != ["Inicio", "Fin"]:
        bad(f"the diagram's node labels are missing (boxes, edges and markers can "
            f"all render with no text in them): {diagrams[0]['labels'] if diagrams else None}")


def check_emoji_text(em: dict[str, Any]) -> None:
    """Emoji in the text become images, a ZWJ sequence stays one, a flag without an asset stays text."""
    if em["state"] != "ok":
        bad(f"the emoji pass did not run (data-emoji={em['state']!r})")
    elif em["inText"] < 6:
        bad(f"only {em['inText']} emoji became images (expected at least 6: the "
            f"characters, the ZWJ sequences, the one in emphasis): {em['geom']}")
    else:
        ok(f"emoji in the text render as FluentUI images ({em['inText']} of "
           f"{em['total']}, data-emoji-count={em['count']}): {em['geom']}")
    # A ZWJ sequence is ONE emoji and has ONE asset: split into its parts it would show
    # a picture of something the answer did not say.
    if em["zwjSplit"]:
        bad(f"a ZWJ sequence was split into separate images: {em['alt']}")
    elif em["zwjWhole"]:
        ok("a ZWJ sequence stays one image (rainbow flag, black cat)")
    else:
        bad(f"the ZWJ sequences did not render as one image each: {em['alt']}")
    # A character with no FluentUI asset (a country flag) must stay a character: it is
    # the font's glyph or nothing, and an <img> pointing at a file that does not exist
    # would be a broken picture.
    if em["flagLeft"]:
        ok("an emoji with no asset (a country flag) stays a character, not a broken image")
    else:
        bad("the country flag was converted or dropped: an emoji with no asset must fall "
            "back to the font's own glyph")


def check_emoji_special(em: dict[str, Any]) -> None:
    """A tag-sequence flag, the keycaps, and the emoji inside code."""
    # A tag-sequence flag shares its base codepoint with the plain black flag, so a
    # sequence that is not consumed whole degrades to the WRONG picture and no lookup
    # miss can be seen. Both halves are asserted.
    if em["blackFlagShown"]:
        bad("the England flag was drawn as the plain black flag: its tag characters were "
            "not part of the match, so the lookup named the base codepoint")
    elif em["tagFlagLeft"]:
        ok("a tag-sequence flag stays its own characters instead of becoming a wrong image")
    else:
        bad("the tag-sequence flag vanished: it was neither converted nor left as characters")
    if em["keycapOk"]:
        ok("multi-codepoint keycaps convert (1️⃣ 2️⃣)")
    else:
        bad(f"the keycaps did not convert — a key that misses looks identical to a "
            f"character with no asset: {em['alt']}")
    if em["inCode"] > 0:
        bad(f"{em['inCode']} emoji were converted INSIDE code: the snippet the reader "
            f"copies is now an <img> instead of a character")
    elif em["codeEmojiLeft"]:
        ok("emoji inside code stay characters, as they must")
    else:
        bad("no emoji was left in the code to prove the skip works: the fixture no "
            "longer exercises it, so this check would pass on a conversion that "
            "walked into code blocks")


def check_emoji_images(em: dict[str, Any]) -> None:
    """The emoji images load, carry no frame and are larger than the text around them."""
    imgs = [g for g in em["geom"] if g["src"]]
    if len(imgs) >= 3 and all(g["loaded"] and g["w"] > 0 and g["h"] > 0 for g in imgs):
        ok(f"the emoji images load and are sized from the font ({imgs[0]['w']}x"
           f"{imgs[0]['h']} for {imgs[0]['src']})")
    else:
        bad(f"the emoji images did not load or have no size: {em['geom']}")
    check_emoji_frame(imgs)
    check_emoji_size(imgs)


def check_emoji_frame(imgs: list[dict[str, Any]]) -> None:
    """No emoji image carries the screenshot rule's border, radius or padding."""
    # An emoji is a CHARACTER, not a framed picture. The generic `.markdown-body img` rule
    # styles a pasted screenshot with a border, a radius and an outer margin, and all three
    # reach an emoji through inheritance of the plain selector — which is a stray box drawn
    # around every emoji in the text.
    framed = [g for g in imgs
              if g["border"] not in ("0px", "") or g["radius"] not in ("0px", "")
              or g["pad"] not in ("0px", "")]
    if not imgs:
        bad("no emoji images to check for a stray frame")
    elif not framed:
        ok(f"an emoji is drawn as a bare character: no border ({imgs[0]['border']}), "
           f"no radius ({imgs[0]['radius']}), no padding ({imgs[0]['pad']})")
    else:
        bad(f"{len(framed)} emoji(s) carry the screenshot rule's box — border "
            f"{framed[0]['border']}, radius {framed[0]['radius']}, padding "
            f"{framed[0]['pad']}: at least one emoji is inside a box")


def check_emoji_size(imgs: list[dict[str, Any]]) -> None:
    """Emoji images are drawn larger than the text around them."""
    # Larger than the text it sits in: an emoji glyph is drawn well above the cap height of
    # the surrounding letters, and an image sized to the font exactly reads as small next
    # to one. Judged against the computed font size rather than a pixel count, so it holds
    # at any font size the reader picks.
    sized = [g for g in imgs if g["h"] >= 1.15 * float(g["font"].replace("px", ""))]
    if imgs and sized:
        ok(f"emoji are drawn larger than the text they sit in ({imgs[0]['h']}px tall "
           f"against a {imgs[0]['font']} font)")
    elif imgs:
        bad(f"emoji are not larger than the text: {imgs[0]['h']}px against a "
            f"{imgs[0]['font']} font (want at least 1.15em)")


def check_emoji(em: dict[str, Any]) -> None:
    """Emoji: images in the text, characters in the code."""
    # --- emoji: images in the text, characters in the code ------------------
    # The first half proves the feature ran; the second is the one that would catch a
    # conversion walking into a code block and corrupting what the reader copies.
    check_emoji_text(em)
    check_emoji_special(em)
    check_emoji_images(em)


def check_alerts(m: dict[str, Any]) -> None:
    """Both callouts render as styled alerts."""
    # --- alerts -----------------------------------------------------------
    kinds = sorted("note" if "note" in a else "warning"
                   for a in m["alerts"] if "markdown-alert" in a)
    if kinds == ["note", "warning"]:
        ok("both callouts render as styled alerts (note + warning)")
    else:
        bad(f"the callouts did not render as alerts: {m['alerts']}")


def check_code(m: dict[str, Any]) -> None:
    """Code is highlighted and both copy payloads survive the sanitizer."""
    # --- code and the copy contract ---------------------------------------
    code = m["code"]
    if code["tokenColor"]:
        ok(f"code is highlighted (a token is painted {code['tokenColor']})")
    else:
        bad("no highlighted token: the code block is plain text")
    if (code["copyTextLen"] is not None and code["codeLen"] > 0
            and code["copyTextLen"] == code["codeLen"]):
        ok(f"the code block's copy button holds its source exactly "
           f"({code['copyTextLen']} chars) even though the snippet contains -->")
    elif code["copyTextLen"]:
        bad(f"the code block's copy payload does not match the code it shows: "
            f"{code['copyTextLen']} chars of payload for {code['codeLen']} chars of code")
    else:
        bad(f"the code block's copy payload is empty ({code['copyTextLen']}) — the "
            f"sanitizer emptied it")
    if m["messageCopyLen"] > 100:
        ok(f"the message's copy payload survives ({m['messageCopyLen']} chars) even "
           f"though it contains -->")
    else:
        bad(f"the message's own copy payload is missing "
            f"({m['messageCopyLen']} chars): copying an answer would paste nothing")


def check_render_errors(m: dict[str, Any]) -> None:
    """Exactly one error box: the deliberately broken diagram."""
    # --- nothing failed to render, EXCEPT the one that is meant to ---------
    # The fixture ends with a malformed diagram on purpose, so exactly one error box is
    # the expected count. Asserting 0 here would be asserting the failure path is never
    # exercised, which is the state that let mermaid's own error icon reach the page.
    if m["renderErrors"] == 1:
        ok("exactly one element is an error box: the deliberately-broken diagram, and "
           "nothing else")
    elif m["renderErrors"] == 0:
        bad("no element reported a render failure, but the fixture contains a malformed "
            "diagram: it failed silently, which is worse than showing the reason")
    else:
        bad(f"{m['renderErrors']} elements rendered as error boxes; only the one "
            f"deliberately-broken diagram should be")


def check_spinner_card(sp: dict[str, Any]) -> None:
    """The spinner announces itself as a status and says what it is doing."""
    if sp.get("role") == "status":
        ok("the spinner announces itself to assistive tech (role=status)")
    else:
        bad(f"the spinner carries no role: {sp.get('role')!r}")
    if sp.get("label"):
        ok(f"the spinner says what it is doing: {sp['label']!r}")
    else:
        bad("the spinner has no label")


def check_spinner_mark(sp: dict[str, Any]) -> None:
    """The spinner draws the requested arc, in the requested colour, turning."""
    # The mark asked for: this exact path, painted in hsl(228, 97%, 42%).
    # That converts to rgb(3, 45, 211) — measured, not assumed: a first version of this
    # check hardcoded the conversion wrong (rgb(3, 14, 231), which is closer to hue 232)
    # and failed a spinner that was painted correctly.
    if (sp.get("d") or "").startswith("M12,23a9.63"):
        ok("the spinner draws the arc that was asked for")
    else:
        bad(f"the spinner's path is not the one asked for: {(sp.get('d') or '')[:40]!r}")
    if sp.get("fill") == "rgb(3, 45, 211)":
        ok(f"the spinner is painted the requested colour ({sp['fill']})")
    else:
        bad(f"the spinner's colour is {sp.get('fill')!r}, not the requested "
            f"hsl(228, 97%, 42%) = rgb(3, 45, 211)")
    if sp.get("rotate") == "0 12 12;360 12 12" and sp.get("dur") == "0.75s" \
            and sp.get("repeat") == "indefinite":
        ok(f"the spinner turns ({sp['rotate']} in {sp['dur']}, {sp['repeat']})")
    else:
        bad(f"the spinner does not turn as asked: values={sp.get('rotate')!r} "
            f"dur={sp.get('dur')!r} repeat={sp.get('repeat')!r}")


def check_spinner_modal(sp: dict[str, Any]) -> None:
    """The spinner sits in a small modal card, not a full-bleed overlay."""
    # A small modal, not a full-bleed overlay: the card has to be much smaller than the
    # area it sits in, which is also what tells the two designs apart in a measurement.
    modal = sp.get("modal") or {}
    if modal.get("w") and modal.get("h") and \
            modal["w"] < 420 and modal["h"] < 140 and modal.get("scrimW", 0) > modal["w"]:
        ok(f"the spinner is a small modal card ({modal['w']}x{modal['h']} inside a "
           f"{modal['scrimW']}x{modal['scrimH']} area), not a full-bleed overlay")
    else:
        bad(f"the spinner is not presented as a small modal: {modal}")


async def check_spinner(c: CDP, shots: str) -> dict[str, Any] | None:
    """The chat spinner covers the conversation while it loads; returns the page's load log."""
    # --- the loading spinner over the conversation -------------------------
    ll = await c.js("window.__loadLog")
    await c.js("window.__loadLogStop && window.__loadLogStop()")
    await save_json(f"{shots}/loadlog.json", ll)
    sp = (ll or {}).get("spinner") or {}
    if not ll or ll.get("seen", 0) < 1:
        bad("the chat spinner never appeared while the conversation was loading")
    elif not sp.get("live"):
        bad("the chat spinner appeared but was detached before it could be measured")
    else:
        ok(f"the chat spinner covers the conversation while it loads ({ll['seen']} sightings, "
           f"{sp['w']}x{sp['h']})")
        check_spinner_card(sp)
        check_spinner_mark(sp)
        check_spinner_modal(sp)
    return ll


def check_deferral_outcomes(obs: dict[str, Any]) -> None:
    """Both visibility outcomes are measured, what is in view is built, and nothing else is."""
    # The load-bearing one. Both halves are needed: an eager build makes every message
    # "in view" (outOfView 0), and an observer that never reported leaves every message
    # at whatever it was initialised to — which looks identical from this side.
    both = obs["inView"] > 0 and obs["outOfView"] > 0
    if both:
        ok(f"visibility is a real measurement with both outcomes ({obs['inView']} in "
           f"view, {obs['outOfView']} out of view) — messages off screen are left UNBUILT, "
           f"so a long conversation does not render every diagram it holds on mount")
    else:
        reads_as = "in view" if obs["outOfView"] == 0 else "out of view"
        bad(f"every one of the {obs['hosts']} message(s) reads as {reads_as}"
            f" (inView={obs['inView']}, outOfView={obs['outOfView']}): either everything "
            f"was hydrated regardless of visibility, or the observer never reported")
    # And the flip side: what the reader IS looking at must be built, or the deferral is
    # just a page that never renders.
    if obs["inViewPending"] == 0:
        ok(f"the message in front of the reader is fully built (nothing in view is still "
           f"pending), with {obs['mermaidDrawn']} of {obs['mermaidBlocks']} diagram(s) "
           f"drawn on the page")
    else:
        bad(f"{obs['inViewPending']} message(s) in view are still unbuilt — the deferral "
            f"never fired for what the reader is looking at")
    # And the other half of the same coin: the messages the reader never passed are still
    # unbuilt, which is the POINT. Asserting that every diagram is drawn would be
    # asserting that everything was hydrated, i.e. that the deferral does nothing.
    if obs["mermaidDrawn"] < obs["mermaidBlocks"]:
        ok(f"only the diagrams the reader reached were built ({obs['mermaidDrawn']} of "
           f"{obs['mermaidBlocks']}): nothing else was paid for")
    else:
        bad(f"every one of the {obs['mermaidBlocks']} diagram(s) was built even though the "
            f"reader jumped past most of them — the deferral did nothing")


async def check_deferral(c: CDP, shots: str) -> None:
    """The deferred work only happens for what is about to be read."""
    # --- the deferred work only happens for what is about to be READ ----------
    # The page must not build every diagram of every turn on mount. The conversation seeded
    # here is deliberately long for this: the filler turns sit far above the fold and must
    # still be UNBUILT when the transcript is up, while the message the reader is on is built.
    #
    # Judged in the DOM (`data-inview`, `data-hydrating`) rather than by counting SVGs: the
    # number of nodes is what is being kept down, so counting them is the measurement, but
    # WHICH ones is a property of the observer. Both are reported.
    obs = await c.js("""(() => {
        // The host element that carries the hydration markers is the one hydrate() received.
        const hosts = [...document.querySelectorAll('[data-hydrating], [data-inview]')];
        const blocks = [...document.querySelectorAll('.mermaid-block')];
        const drawn = blocks.filter(b => b.querySelector('svg'));
        // "The reader sees a diagram" is about what is on SCREEN, so the drawn blocks are
        // counted by whether they are inside the viewport — not merely present in the DOM,
        // which is what the deferral makes meaningful in the first place.
        return {
          hosts: hosts.length,
          // The SAME mark the observer maintains, read as the count on the page: this is
          // what the spinner's visible-pending question is answered from, so it is the
          // attribute that has to be right in both directions.
          inView: hosts.filter(h => h.getAttribute('data-inview') === '1').length,
          outOfView: hosts.filter(h => h.getAttribute('data-inview') === '0').length,
          stillPending: hosts.filter(h => h.hasAttribute('data-hydrating')).length,
          // The message the reader is ON: is anything in it still unbuilt? That is the
          // requirement, and it is not the same question as "is a diagram visible" — the
          // block in front of the reader here is the deliberately-broken one, which must
          // NOT be drawn.
          inViewPending: hosts.filter(h => h.getAttribute('data-inview') === '1'
                                          && h.hasAttribute('data-hydrating')).length,
          mermaidBlocks: blocks.length,
          mermaidDrawn: drawn.length,
          scrollTop: Math.round((document.querySelector('main[role="log"]') || {}).scrollTop || 0),
        };
    })()""")
    await save_json(f"{shots}/observer.json", obs)
    if not obs or obs.get("hosts", 0) < 2:
        bad(f"no messages registered for deferred work: {obs}")
    else:
        ok(f"{obs['hosts']} message(s) registered for deferred hydration "
           f"({obs['inView']} in view, {obs['outOfView']} out of view)")
        check_deferral_outcomes(obs)


async def wait_modal_cleared(c: CDP) -> bool | None:
    """Wait up to ~20 s for the session modal to come down; returns whether it did."""
    # --- the modal waits for the page to STOP, and does not flicker --------------
    #
    # The design is a QUIET WINDOW: activity restarts a 2s timer, and the modal only comes down
    # when that timer runs out. So the reader's question ("did it wait for everything?") and the
    # flicker question are answered by the same two measurements:
    #
    #   * was the modal taken down at all, and only once nothing visible was still being built;
    #   * how many times was it PUT UP — one is the design working, one per answer is the
    #     "loading, finishing, loading again" the reader reported.
    cleared_now = None
    for _ in range(40):
        cleared_now = await c.js("document.querySelector('.chat-modal-scrim') === null")
        if cleared_now:
            break
        await asyncio.sleep(0.5)
    return cleared_now


def loading_windows(vis_log: list[dict[str, Any]] | None) -> list[list[dict[str, Any]]]:
    """The runs of consecutive loading samples: one per loading window."""
    runs, current = [], []
    for sample in (vis_log or []):
        if sample.get("loading") == "1":
            current.append(sample)
        elif current:
            runs.append(current)
            current = []
    if current:
        runs.append(current)
    return runs


async def check_hidden_while_loading(c: CDP, vis_log: list[dict[str, Any]] | None) -> None:
    """The conversation is held at opacity 0 through each whole loading window, then revealed."""
    hidden = [x for x in (vis_log or []) if float(x.get("opacity", 1)) == 0]
    while_loading = [x for x in (vis_log or []) if x.get("loading") == "1"]
    # Two things this must NOT claim, both of which a naive reading gets wrong:
    #
    #   * The page is legitimately at full opacity BEFORE a session starts loading (measured:
    #     opacity 1, loading 0, no modal at 50ms). That is not a "reveal", so ordering the two
    #     states against each other tests nothing.
    #   * The fade is intentional (`transition: opacity 180ms`), so the first frames after
    #     loading begins are still on their way down (measured 1 -> 0.46 -> 0.099 -> 0 over
    #     ~150ms). What the reader must never see is a FULLY-opaque transcript while the parse
    #     is still running, once the fade has had time to finish.
    fade_ms = await c.js("""(() => {
        const el = document.querySelector('main[role="log"]');
        if (!el) return 0;
        const d = getComputedStyle(el).transitionDuration || '0s';
        // '0.42s' or '420ms'; take the first value and normalise to ms.
        const first = d.split(',')[0].trim();
        const n = parseFloat(first) || 0;
        return Math.round(first.endsWith('ms') ? n : n * 1000);
    })()""")
    # The allowance is the fade PLUS a margin for the frame cadence: the assertion is that the
    # reader never sees the transcript at full opacity while it is being built, not that the
    # browser finishes a CSS transition on an exact millisecond.
    FADE_MS = (fade_ms or 0) + 150
    settled = (vis_log or [{}])[-1]
    if not vis_log:
        bad("nothing recorded the conversation's visibility while the page loaded")
    elif not while_loading:
        bad(f"the conversation never reported itself as loading: {vis_log}")
    elif not hidden:
        bad(f"the conversation was NEVER held at opacity 0 while it loaded - the reader "
            f"watches it change shape. Transitions: {vis_log}")
    else:
        # Each LOADING WINDOW is judged on its own, because there is more than one in a real
        # session (the transcript being replaced, then the conversation being opened) and a
        # threshold computed from the first window's start flagged every sample of the second
        # one as "still visible". A window is a run of consecutive loading samples with no
        # settled sample between them.
        runs = loading_windows(vis_log)
        offenders = visible_late(runs, FADE_MS)
        if offenders:
            bad(f"the conversation is still visible {FADE_MS}ms into a loading window, so the "
                f"reader watches it change shape: {offenders}")
        elif settled.get("loading") != "0" or float(settled.get("opacity", 0)) < 0.9:
            bad(f"the conversation was never revealed after the work finished: {settled}")
        else:
            ok(f"the conversation is held at opacity 0 for the whole parse and revealed only "
               f"when the work is over ({len(runs)} loading window(s), hidden from "
               f"{hidden[0]['at']}ms, revealed at {settled['at']}ms, allowance {FADE_MS}ms "
               f"read from the CSS)")


def visible_late(runs: list[list[dict[str, Any]]], allowance: float) -> list[dict[str, Any]]:
    """The samples still visible more than allowance ms into their loading window."""
    offenders = []
    for run in runs:
        begun = run[0]["at"]
        offenders += [x for x in run
                      if x["at"] > begun + allowance and float(x.get("opacity", 0)) > 0.05]
    return offenders


def reveal_instant(frames: list[dict[str, Any]] | None) -> int | None:
    """When the chat became readable: the first opaque, settled frame after the last loading one."""
    loading_ats = [f["at"] for f in (frames or []) if f.get("loading") == "1"]
    reveal_at = None
    if loading_ats:
        last_loading = max(loading_ats)
        for f in (frames or []):
            if (f["at"] > last_loading and f.get("chat") is not None
                    and float(f["chat"]) > 0.9 and f.get("loading") == "0"):
                reveal_at = f["at"]
                break
    return reveal_at


def layout_changes(first: dict[str, Any], last: dict[str, Any]) -> dict[str, str]:
    """Every height or position that differs between two visibility-log samples."""
    changed = {}
    for k in ("contentHeight", "boxHeight", "clientHeight", "docHeight",
              "innerHeight", "parentClient"):
        a, b = first.get(k), last.get(k)
        if a is not None and b is not None and a != b:
            changed[k] = f"{a} -> {b}"
    for name in ("parent", "header", "aside"):
        a, b = first.get(name), last.get(name)
        if a and b and (a["h"] != b["h"] or a["top"] != b["top"]):
            changed[name] = f"h {a['h']} -> {b['h']}, top {a['top']} -> {b['top']}"
    return changed


async def check_resize_after_reveal(frames: list[dict[str, Any]] | None, vis_log: list[dict[str, Any]] | None,
                                    shots: str) -> None:
    """Nothing changes size after the chat becomes readable."""
    # --- what changes size AFTER the reveal -------------------------------------
    #
    # The failure the reader still sees: it finishes loading, it looks right, and then something
    # changes the height and the scroll moves. This names the element and the amount, so the
    # cause is measured rather than guessed.
    # Its own computation of the reveal instant: this block sits ABOVE the one that defines
    # `reveal_at`, and reaching for it there raised UnboundLocalError - the probe printed no
    # verdict at all, which the gate reports as a failure with no measurement behind it.
    _rev_at = reveal_instant(frames)
    if _rev_at is not None:
        after = [x for x in (vis_log or []) if x["at"] >= _rev_at]
        if len(after) >= 2:
            first, last = after[0], after[-1]
            changed = layout_changes(first, last)
            await save_json(f"{shots}/resize-after-reveal.json",
                            {"first": first, "last": last, "changed": changed}, ensure_ascii=True)
            if not changed:
                ok("nothing changes size after the chat becomes readable "
                   "(the conversation was already at its final height)")
            else:
                bad(f"the layout still changes AFTER the chat is readable: "
                    f"{changed} - the view is re-aimed and the reader sees it move")
        else:
            bad(f"only {len(after)} sample(s) after the reveal: the aftermath was not measured")


async def check_scroll_after_reveal(frames: list[dict[str, Any]] | None, shots: str) -> None:
    """The view is already at rest when the chat becomes readable."""
    # --- nothing moves the view AFTER the reveal --------------------------------
    #
    # The reader's second report: the chat appears and then "the height readjusts and it ends up
    # wrong". That is a scroll still travelling once the content is visible, and an end-state
    # check cannot see it - the final position is correct either way. Sample the position across
    # the reveal and require it to stop.
    # Only frames AFTER the last loading sample count: the page is legitimately visible before
    # a session starts loading (measured: opacity 1, loading 0), so the first "revealed" frame is
    # the empty shell and measuring from it reported the whole load as movement.
    reveal_at = reveal_instant(frames)
    if reveal_at is None:
        bad("the reveal never happened in the recording, so its aftermath was not measured")
    else:
        samples = [f for f in (frames or [])
                   if f["at"] >= reveal_at and f.get("scrollTop") is not None]
        await save_json(f"{shots}/scroll-after-reveal.json", samples)
        positions = [x["scrollTop"] for x in samples]
        if not positions:
            bad("no scroll position was recorded after the reveal")
        else:
            span = max(positions) - min(positions)
            # A scroll animation shows as a sequence of increasing positions after the reveal;
            # an exact landing shows one value, or a couple of pixels of sub-pixel rounding.
            if span <= 2:
                ok(f"the view is already at rest when the chat becomes readable "
                   f"({span}px of movement across {len(positions)} sample(s), "
                   f"scroll-behavior: {samples[0].get('scrollBehavior')})")
            else:
                bad(f"the view keeps moving AFTER the chat is revealed ({span}px across "
                    f"{len(positions)} sample(s): {positions[:8]}) - that is the height "
                    f"readjusting the reader reported")


def steps(values: list[Any]) -> list[float]:
    """The distinct values of a sequence of opacities, in order (a change under 0.01 is noise)."""
    seen = []
    for v in values:
        f = float(v)
        if not seen or abs(f - seen[-1]) > 0.01:
            seen.append(f)
    return seen


async def check_transitions(frames: list[dict[str, Any]] | None, shots: str) -> None:
    """The modal leaves and the chat arrives, both gradually."""
    # --- the modal must LEAVE and the chat must ARRIVE, both gradually -----------
    #
    # Counted from per-frame opacity samples: a transition shows several intermediate values, a
    # jump shows one. The reader reported "it appears very suddenly", which is exactly one frame
    # at the new value, so the assertion counts DISTINCT values strictly between the ends.
    modal_out = steps([x["modal"] for x in (frames or []) if x.get("modal") is not None])
    chat_in = steps([x["chat"] for x in (frames or []) if x.get("chat") is not None])
    intermediate_modal = [v for v in modal_out if 0.02 < v < 0.98]
    intermediate_chat = [v for v in chat_in if 0.02 < v < 0.98]
    await save_json(f"{shots}/transitions.json", {"modal": modal_out, "chat": chat_in, "frames": frames})

    if not frames:
        bad("no per-frame opacity samples were recorded, so nothing about the transitions "
            "was measured")
    else:
        if len(intermediate_modal) >= 2:
            ok(f"the modal fades OUT rather than vanishing ({len(intermediate_modal)} "
               f"intermediate opacity value(s): "
               f"{[round(v, 2) for v in intermediate_modal]})")
        else:
            bad(f"the modal disappears in one frame - no fade-out was measured. Opacity "
                f"sequence: {modal_out}")
        if len(intermediate_chat) >= 2:
            ok(f"the chat fades IN rather than appearing at once "
               f"({len(intermediate_chat)} intermediate opacity value(s): "
               f"{[round(v, 2) for v in intermediate_chat]})")
        else:
            bad(f"the chat appears at full opacity in one frame - no fade-in was measured. "
                f"Opacity sequence: {chat_in}")


async def check_scroll_behavior(c: CDP) -> None:
    """The conversation container does not smooth-scroll."""
    behavior = await c.js("""(() => {
        const el = document.querySelector('main[role="log"]');
        return el ? getComputedStyle(el).scrollBehavior : null;
    })()""")
    if behavior is None:
        bad("the conversation container was not found when measuring its scroll behaviour")
    elif behavior == "smooth":
        bad(f"the conversation container smooth-scrolls (scroll-behavior: {behavior}), so every "
            f"programmatic landing is animated and the next layout change catches it mid-flight")
    else:
        ok(f"the conversation container scrolls exactly (scroll-behavior: {behavior})")


def check_content_grew(vis_log: list[dict[str, Any]] | None) -> None:
    """The content grew behind the modal, so the hidden phase was exercised."""
    # And the height it settled at: the hidden phase is what keeps the reader from watching it
    # grow, so the two numbers together are the whole story.
    if vis_log:
        first_h = (vis_log[0].get("contentHeight") or 0)
        last_h = (vis_log[-1].get("contentHeight") or 0)
        if last_h > first_h:
            note(f"the conversation's content grew {first_h}px -> {last_h}px behind the modal, "
                 f"which is exactly what the reader is spared from watching")
        else:
            bad(f"the conversation's content never grew ({first_h}px -> {last_h}px): the "
                f"hidden phase and the final scroll are then untested")


async def check_revealed(c: CDP, cleared_now: bool | None, shots: str) -> None:
    """Once the page went quiet, the conversation is revealed."""
    # --- the conversation is HIDDEN while it is being built ---------------------
    #
    # Measured on the computed style of the real container, not on a class name: the reader
    # must not watch formulas arrive and diagrams grow, and the scroll must not chase a height
    # that is still changing.
    vis = await c.js("""(() => {
        const el = document.querySelector('main[role="log"]');
        if (!el) return null;
        const cs = getComputedStyle(el);
        const r = el.getBoundingClientRect();
        // opacity 0 on an element is only meaningful if it is really painted, so this also
        // reports what is behind it: the reader sees the modal, not a half-built transcript.
        return { opacity: cs.opacity, loading: el.getAttribute('data-chat-loading'),
                 width: Math.round(r.width), height: Math.round(r.height),
                 modal: !!document.querySelector('.chat-modal-scrim') };
    })()""")
    await save_json(f"{shots}/visibility.json", vis)
    if not vis:
        bad("the conversation container was not found, so nothing about its visibility "
            "was measured")
    elif cleared_now:
        if vis.get("loading") == "0" and float(vis.get("opacity", 0)) > 0.9:
            ok(f"the conversation is revealed once the page went quiet "
               f"(opacity {vis.get('opacity')}, {vis.get('width')}x{vis.get('height')}px)")
        else:
            bad(f"the page went quiet but the conversation is still hidden: {vis}")
    else:
        bad("the conversation was never revealed")


def check_modal(cleared_now: bool | None, ch: dict[str, Any] | None, ml: list[dict[str, Any]] | None) -> None:
    """The session modal came down only once the page went quiet, and was not put up per answer."""
    if not cleared_now:
        bad("the session modal was still up 20s after the transcript settled: the quiet window "
            "never expired, so the reader is left looking at it")
    else:
        ok("the session modal came down, and only after the page went quiet")
        if (ch or {}).get("visiblePending") is False:
            ok(f"the whole visible conversation was built by then "
               f"({(ch or {}).get('registered', 0)} message(s) registered, nothing visible pending)")
        else:
            bad(f"the modal came down with visible work still outstanding: {ch}")

    puts = [x for x in (ml or []) if x.get("loading")]
    if not puts:
        bad(f"the modal was never put up at all: {ml}")
    elif len(puts) <= 2:
        ok(f"the session modal was put up {len(puts)} time(s) for the whole conversation "
           f"({len(ml or [])} transition(s)): it follows the page going quiet, not each answer")
    else:
        bad(f"the session modal was put up {len(puts)} time(s) — that is the flicker the reader "
            f"reported: loading, finishing, loading again. Transitions: {ml}")


async def check_observer(c: CDP, shots: str) -> None:
    """The IntersectionObserver reported, with both outcomes."""
    # And the observer has to be the thing deciding, not a default: it must have reported.
    io = await c.js("window.__ioLog")
    await save_json(f"{shots}/io.json", io)
    if not io or io.get("callbacks", 0) < 1:
        bad(f"the IntersectionObserver never reported: the visibility of a message is "
            f"whatever it was initialised to, not a measurement: {io}")
    else:
        seen_hit = sum(1 for d in io.get("decisions", []) if d.get("hit"))
        seen_miss = sum(1 for d in io.get("decisions", []) if not d.get("hit"))
        ok(f"the intersection observer reported {io['callbacks']} time(s) over "
           f"{len(io.get('decisions', []))} observation(s) — {seen_hit} seen, {seen_miss} not "
           f"seen (rootMargin {((io.get('opts') or {}).get('rootMargin'))})")
        if seen_miss > 0:
            ok(f"the observer really does report both outcomes ({seen_miss} observation(s) "
               f"outside the margin), so a message is only built on a real measurement")
        else:
            bad("the observer never reported a single observation as outside the viewport: "
                "the deferral has not been demonstrated, only configured")


async def check_follows(c: CDP, ll: dict[str, Any] | None) -> None:
    """The page follows the deferred work down instead of stranding the reader above it."""
    # --- the page follows the deferred work instead of stranding itself ----
    # Formulas, diagrams and emoji images are built after the message is rendered, and
    # each one changes the scroll height. A container that only reacts to state stops
    # wherever that work first left it and the reader has to drag it down by hand.
    #
    # Measured as: how much the container GREW while loading — if the deferred work really
    # does resize the page, that number is large, and it is what makes the check
    # meaningful rather than a tautology about a page that never changed size.
    heights = (ll or {}).get("heights", [])
    grew = (max(heights) - min(heights)) if len(heights) > 1 else 0
    if grew >= 40:
        ok(f"the deferred work resizes the conversation ({min(heights)}px -> {max(heights)}px, "
           f"+{grew}px while loading) - so staying at the bottom is a real requirement")
    else:
        bad(f"the conversation barely changed height while loading ({heights}); the scroll "
            f"check below would prove nothing")
    m2 = await c.js("""(() => {
        const el = document.querySelector('main[role="log"]');
        if (!el) return {error: 'no conversation container'};
        return { atBottom: el.scrollHeight - el.scrollTop - el.clientHeight,
                 scrollHeight: el.scrollHeight, clientHeight: el.clientHeight };
    })()""")
    if "error" in m2:
        bad(m2["error"])
    elif m2["scrollHeight"] > m2["clientHeight"] and m2["atBottom"] <= 2:
        ok(f"the conversation ends at its last line with no scrolling left to do "
           f"({m2['atBottom']}px below the viewport, {m2['scrollHeight']}px of content)")
    else:
        bad(f"the conversation is not scrolled to the bottom: {m2['atBottom']}px of content "
            f"is stranded below the fold ({m2['scrollHeight']}px tall, "
            f"{m2['clientHeight']}px visible)")


async def check_loading(c: CDP, shots: str) -> None:
    """Everything about putting the conversation on screen: modal, visibility, scroll, observer."""
    ll = await check_spinner(c, shots)
    await check_deferral(c, shots)
    cleared_now = await wait_modal_cleared(c)
    vis_log = await c.js("window.__visLog || []")
    frames = await c.js("window.__frameLog || []")
    await save_json(f"{shots}/visibility-log.json", vis_log)
    await check_hidden_while_loading(c, vis_log)
    await check_resize_after_reveal(frames, vis_log, shots)
    await check_scroll_after_reveal(frames, shots)
    await check_transitions(frames, shots)
    await check_scroll_behavior(c)
    check_content_grew(vis_log)

    ch = await c.js("window.__chatLoadingLog && window.__chatLoadingLog()")
    ml = await c.js("window.__modalLog || []")
    await save_json(f"{shots}/chatloading.json", {"state": ch, "modal": ml})
    await check_revealed(c, cleared_now, shots)
    check_modal(cleared_now, ch, ml)
    await check_observer(c, shots)
    await check_follows(c, ll)
