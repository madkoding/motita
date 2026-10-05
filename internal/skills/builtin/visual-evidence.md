# Visual evidence: screenshots of how it looked before and after

Use when the change is something you can SEE (a layout, a colour, a component, a page) and the
person would trust a picture more than a sentence. Skip it for anything with no visual effect.

## Ask before installing a browser driver

Playwright and its browser are a large download (hundreds of MB). Never install them silently.

1. `command -v playwright; ls <tools dir>/tools` — use what is already there. A Chromium may
   already exist (check `$PLAYWRIGHT_BROWSERS_PATH`, `/opt/pw-browsers`, `chromium`).
2. If nothing is there, ASK the person first, in one question: what you want to install
   (Playwright + Chromium), roughly how big it is, where it goes, and that it is only to take the
   before/after screenshots. Proceed only on a yes. On a no, say so in the report and describe the
   change in words.
3. Install under `<tools dir>/tools/`, never into the repository (see "installing a toolchain").

## Take BOTH halves, in this order

1. BEFORE: capture the page at the unchanged code FIRST (`git stash`, or the base commit in a
   temporary worktree), then restore your change. Never recreate a "before" from memory.
2. Change the code.
3. AFTER: capture the same page, same viewport, same state.

Same URL, same viewport (e.g. 1280x800), same scroll and data, so the only difference is the change.
Use `fullPage: false` unless the whole page changed; crop to the element when it is small.

## Where the files go and how they are named

Save them in `.motita/artifacts/` as PNG: `<what>-before.png` and `<what>-after.png`. Names are
plain file names; no folders, no spaces. Keep each under 2 MB (smaller viewport or `clip`).

Then list them in the report's `evidence` field, one entry per comparison, with a title and a
one-line caption saying what to look at. If only one side can exist (a new screen), leave the
other empty. Never reference a file you did not save.
