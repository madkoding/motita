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

Everything the checklist called a gap is now closed except one, which is closed by
explanation rather than by code.

- **Resize is live, and it took a second attempt to be honest about it.** The first
  version was built, tested and REMOVED, because it could not work: a child environment
  is copied at exec, so a running process's `COLUMNS` never changes and repainting on
  SIGWINCH redrew an identical frame — measured, raised at 60×20, not a pixel moved. The
  missing half was never the signal, it was the measurement. The size now comes from the
  terminal driver via `stty size` (a TIOCGWINSZ ioctl performed for us, in pure Go with
  no cgo and no unsafe), and the environment is the fallback. **Verified on Uchikoma: the
  panel went 99 → 71 → 111 columns in response to external `stty` calls, with no
  keystroke.** Order of trust: explicit override, driver, environment — each asserted in
  its own test.
- **No mouse REQUIRED, as the guide specifies.** The wheel scrolls, reported through the
  SGR protocol: buttons 64/65, modifiers stripped, and every other shape — a click, a
  drag, a release, a malformed report — ignored rather than read as movement. It is
  additive; the keyboard is still the only path that is guaranteed.
- **The search shortcut cannot be guaranteed, so there are two.** `Ctrl+F` is a bonus: a
  terminal in canonical mode consumes control bytes itself, and on Uchikoma the keystroke
  never arrived. `/find <text>` goes through the ordinary line reader and is the path that
  works everywhere. Both are documented, and the help says which is which.
- **The palette is 8 SGR codes**, measured on a real frame and excluding the wordmark's
  own artwork (which the user supplied and which carries its colours by design). The
  guide budgets 3-4 visible at once; each of the eight carries meaning today, so this
  stays a deliberate choice rather than an oversight.

## What the third pass added

- **Conversation search** with live filtering, the match count, highlighted matches (the
  match is located on a lowercased copy but the ORIGINAL text is emitted — colouring a
  lowercased copy would silently rewrite what the agent said), and a designed
  no-matches state that says what was searched plus the two ways out.
- **`/find <text>`**, the typed form that survives a canonical-mode terminal.
- **The mouse wheel**, additive and strictly parsed.
- **`tui.IsTerminal`**, unified: the "is anything watching that can interpret escapes"
  question had two implementations under two names, one for colour and one for the mouse.

## Score after the second pass

| Category | Score | Change |
|---|---|---|
| Visual design | 4 | — |
| Layout and composition | 4 | — |
| Information architecture | 4 | — |
| Interaction design | 5 | Half-page scroll, search, mouse |
| State handling | 4 | — |
| Navigation | 4 | — |
| Component quality | 4 | — |
| Accessibility | 5 | Status differs by shape, not only colour |
| Architecture and code quality | 4 | — |
| Terminal resilience | 5 | Live resize, measured from the driver |
| **Total** | **45/50** | *Production premium — ship it.* |

## What the second pass fixed

- **The status glyph differs by SHAPE, not only colour.** `•` when ready, `°` when
  there is no key, a spinner frame while running. With colour off, on a monochrome
  console, or to a colour-blind reader, the line still means something — which is the
  accessibility failure the guide names explicitly, and the last P1.
- **`Ctrl+U` / `Ctrl+D` scroll half a page**, the vim pair the guide lists. They arrive
  as control bytes, so they are intercepted in the reader and matched before any
  normalisation — the same trap as Tab, which is now the third time that trap has bitten
  in this file.
- **The status no longer contradicts itself.** It said `ready` next to `key missing`.
  The dot and the word are computed from the same condition, so they cannot disagree.
- **A test fixture taught its own lesson**: `fakeRunner.Config()` substitutes
  `config.Default()` whenever no configuration was supplied, so a test that assigned
  `cfg` without setting `cfgSet` silently asserted against the defaults. The failure
  looked like a production bug in the status line and was a fixture swallowing the
  case under test.


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
