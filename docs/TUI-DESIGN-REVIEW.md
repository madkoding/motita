# Starlight TUI — design review

An audit of the terminal interface against the **TUI design guide**
(`tui-design` skill, Workflow 3: *redesign an existing TUI*) and its review
checklist. It records what was wrong, what changed, and what is still open, so the
next pass does not have to rediscover any of it.

## How this was reviewed

The guide's own instruments, not opinion:

- `references/review-checklist.md` — ten scored categories, and the P0/P1/P2/P3
  priority list the fixes below are ordered by.
- `references/design-principles.md` — hierarchy, spacing, alignment, borders.
- `references/component-catalog.md` — the status bar, tabs, empty state and
  progress patterns the interface is built from.
- `references/interaction-guide.md` — the keybinding conventions and the focus
  rules.
- `references/states-and-feedback.md` — empty, loading, error and interactive.
- `references/color-and-emphasis.md` — the 3-4 colour budget and `NO_COLOR`.

Every change was verified on the target hardware (Uchikoma, Atom N270, 100×30),
by replaying the captured ANSI through an emulator and asserting on the grid —
never by reading the raw escapes.

## Score before the pass

| Category | Score | Why |
|---|---|---|
| Visual design | 4 | One border used once; restrained palette; wordmark unframed. |
| Layout and composition | 2 | **No minimum-size handling**; a short terminal shed every part and still drew. |
| Information architecture | 4 | Modes are clear, the status line answers what you are talking to. |
| Interaction design | 2 | **No scrolling at all**; **no Escape**; the footer hid the scroll keys that did not exist. |
| State handling | 4 | Designed empty state, spinner, errors reported in the chat. |
| Navigation | 3 | Modes clear, but history was unreachable and there was no escape route. |
| Component quality | 3 | Truncation and wrapping handled; no position indicator for a long conversation. |
| Accessibility | 2 | `NO_COLOR` was **implemented but never wired**; colour-only status dot. |
| Architecture and code quality | 4 | Render/state/input separated; seams injectable. |
| Terminal resilience | 3 | Height shedding, one-column margin; **no undersized gate**; resize only on restart. |
| **Total** | **31/50** | *Functional prototype — needs significant UX work.* |

## What was fixed

### P0 — keyboard navigation (interaction design, navigation)

The guide: *"All navigation and actions must work via keyboard"* and *"any state the
user can enter, they must be able to exit."* The conversation could only ever show
its newest rows.

- `↑ ↓`, `PgUp PgDn`, `Home End`, `j k`, `g G` now move a window anchored to the
  newest line.
- The offset counts rows **above the newest line**, so output arriving below it does
  not move a view the reader lifted; a new turn returns it to the bottom.
- `Esc` cancels a running turn, or returns to the newest line when there is nothing
  to cancel.
- The offset is clamped to `body − rows_on_screen`, not `body − 1`: the last
  screenful is not a position the window can occupy, and off-clamping hid the only
  message in a short conversation.

### P1 — the position indicator (component quality)

A conversation longer than the window gave no clue where you were. The panel border
now carries the position as a percentage, and the status line states the exact
offset as `• scrolled N above latest`.

**The bug this exposed:** the indicator carries colour escapes, and the border
arithmetic subtracted it by **byte length** instead of visible width. The frame was
correct at the bottom of the conversation and broke the moment it scrolled —
leaving a second `┐` in the middle of the top border:

```
┌─ Plan ─100%─┐─────────────────────────────────────┐     before
┌─ Plan ──────────────────────────────100%─┐              after
```

Fixed by measuring both ends with `visibleLen`, the same function that pads every
other row, so the arithmetic cannot drift from what is drawn. The test asserts the
invariant at the minimum, default and maximum width *with the indicator shown* —
the case the original width test never exercised — and was proved non-vacuous by
reintroducing the bug and watching it fail.

### P1 — `NO_COLOR` was a dead feature (accessibility)

`TUI.NoColor` existed and every test set it, but the production wiring never did:
`NO_COLOR=1` and `TERM=dumb` had no effect on the real program. Now honoured, along
with a non-file output writer — nothing on the other side of a pipe is known to
interpret escapes, and sending them to a log leaves the sequences as literal noise.

### P1 — undersized terminals (layout, terminal resilience)

Below eight rows the interface drew a frame with every droppable part removed. It now
explains the size it needs and the size it has, and the cursor is not parked after a
prompt that was never drawn.

### Housekeeping

- Two unreachable branches were **deleted rather than tested**: a clamp that
  `minWidth` makes impossible, and a `G` case the lowercasing turned into dead code
  (`G` is now matched against the raw line, before normalisation).
- The footer advertises `j/k scroll`, a binding that exists. A test presses every
  advertised key against the handler: a hint for a key that does nothing is a lie the
  user finds in seconds.
- The help screen documents the navigation keys, not only the commands.

## Score after the pass

| Category | Score | Change |
|---|---|---|
| Visual design | 4 | — |
| Layout and composition | 4 | Minimum-size gate |
| Information architecture | 4 | — |
| Interaction design | 4 | Scrolling, Escape route, hints that exist |
| State handling | 4 | — |
| Navigation | 4 | History reachable, position indicator |
| Component quality | 4 | Position indicator, invariant tested |
| Accessibility | 4 | `NO_COLOR` wired, `TERM=dumb` honoured |
| Architecture and code quality | 4 | Dead code removed |
| Terminal resilience | 4 | Undersized gate, still one column short |
| **Total** | **40/50** | *Good with polish needed.* |

## Still open — the honest list

- **Resize is not live.** The frame is measured from `COLUMNS`/`LINES` on every
  repaint, so a terminal that re-exports them is picked up, but nothing watches for
  a resize signal (SIGWINCH) and there is no redraw on demand. Interrogating the
  terminal needs an ioctl through `unsafe`, which the standard library cannot
  express portably across linux/windows/darwin. **P2.**
- **No mouse support.** The guide calls it additive, and nothing requires it, so this
  is a fair omission rather than a gap. **P2.**
- **No search through the conversation** (`/` is taken by the mode shortcuts). A long
  session would benefit from a filter, but no user has needed it yet. **P2.**
- **Colour-only status dot.** The dot is paired with a word (`ready` / `running`),
  so it is not colour alone, but the glyph itself is the same shape in every state.
  A distinct symbol per state would be stronger. **P1.**
- **Seven palette constants** where the guide budgets 3-4 visible at once. Not all
  appear on every screen, but the palette could be tightened. **P2.**
- **No `Ctrl+D`/`Ctrl+U` half-page scroll**, though the guide lists them. **P2.**

None of these is a correctness problem; they are the difference between "good" and
"delightful" on the guide's scale, and they are listed so nobody has to audit this
again to find them.

## Verification

- `gofmt`, `go vet`, `go test -race ./...` clean; **every package at 100% coverage**
  (the CI gate).
- `scripts/e2e.sh` and `scripts/e2e-agent.sh` green on linux/386, amd64, arm, arm64.
- On Uchikoma: the wordmark unframed, the panel 99 columns uniform with the border
  straight, `100%` in the border while scrolled, the cursor parked on row 29 at the
  prompt, and real answers produced by real tools.
