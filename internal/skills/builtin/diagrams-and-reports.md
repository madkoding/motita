# Diagrams and reports: showing the work

Use when the answer's job is to make something LEGIBLE rather than merely correct: showing which
branches changed and what files moved, laying out a plan someone has to approve, or reporting a
change so it can be seen at a glance. A wall of prose buries the result; a diagram or a
side-by-side carries it in one look.

## Where your answer is READ decides what you can use

The same text is drawn by a browser (the gateway web interface) and by the terminal (the TUI).
They are not equal, and a diagram sent to the wrong one costs the reader the whole answer.

| Surface | Markdown | ` ```mermaid ` | Inline HTML/SVG |
| --- | --- | --- | --- |
| **Web interface** | rendered (CommonMark + GFM) | drawn | kept, minus the dangerous parts |
| **TUI** | NOT rendered — plain text, wrapped | **not** drawn | **not** kept — shown as literal tags |

So in a terminal, a diagram block arrives as its own source code and an HTML report arrives as a
wall of tags. Both are worse than the prose they replaced. **When the surface is a terminal, say
the structure in text** — an indented tree, a numbered list, an aligned table — and keep the
diagram for a surface that draws it, or put the diagram's meaning in a sentence as well.

## Diagrams: write the type the question has

Every mermaid family is available (the full library is loaded, not a subset), and these are the
ones that carry engineering work. Each is verified to draw, labels included:

| Question | Type | Fence |
| --- | --- | --- |
| What is the shape of the change? | `flowchart` | ` ```mermaid ` |
| What happened in time / in what order? | `sequenceDiagram` | |
| What are the types and how do they relate? | `classDiagram` | |
| What states can this thing be in? | `stateDiagram-v2` | |
| What is planned, and when? | `gantt` | |
| What did the branches do? | `gitGraph` | |

Rules that keep a diagram readable instead of decorative:

- **One idea per diagram.** Two questions means two diagrams, not one crowded graph.
- **Label the edges, not just the boxes.** `A --> B` says nothing; `A -->|valida| B` says what
  the relationship IS.
- **Keep node text short.** A sentence in a box is a paragraph with a frame around it.
- **No emoji inside a diagram's labels.** The renderer sizes a box for the glyph it expects, and
  an emoji comes out wider than the space reserved.
- **A diagram is never the whole answer.** One or two sentences saying what it shows go with it,
  or a reader who cannot see it has nothing.

## Plans: steps first, then the shape of the plan

A plan is approved or rejected on its ORDER and its RISKS, and both are clearer outside prose.

1. Write the steps as a numbered list, one line each: the action, the object, the check that
   says it worked. A step nobody can verify is not a step.
2. Then show the dependency between them as a diagram — a `flowchart` for what blocks what, a
   `gantt` when the work has duration or a deadline.
3. Then the risks: what could break, what it costs to undo, what you will do instead if it does.

For anything with types, an interface or a boundary being crossed, a `classDiagram` is the honest
picture: the boxes are the parts and the arrows are the calls. It is UML, it renders, and it
settles arguments about coupling that three paragraphs only make longer.

## Reporting a change: before and after, side by side

"When it is done" is not a claim, it is a comparison. Show it.

- **A table with the numbers on both sides.** `| before | after | measurement |` — and the last
  column is what makes it evidence rather than an impression.
- **The command's real output**, in a fenced block with its language, for anything you ran.
- **A diff fence** for a change to text: ` ```diff ` colours the `-` and `+` lines and makes the
  edit scannable. (Measured: the diff is highlighted as one block — `diff` is not a registered
  grammar in this interface, so the colouring is not per-line; the `-`/`+` prefixes still carry
  the meaning, so write them.)
- **A visual for a change you can see** — a layout, a colour, a size, a chart — and the
  before/after only helps if BOTH halves are there.
- **Never a number you did not measure.** A before/after table is where an invented figure does
  the most damage, because the format itself implies it was observed. If you did not run it, say
  what you did instead.

## Inline HTML and SVG are allowed, and the useful parts survive

The web interface keeps inline HTML and inline SVG in an answer. **Measured** on this renderer,
not assumed:

| Written | Kept? |
| --- | --- |
| `<div style="display:flex;gap:12px">`, colours, borders, widths | **yes** — `style` is allowed and the computed style is real |
| `<section>`, `<h4>`, `<details><summary>`, `<kbd>`, `<mark>`, `<sub>`, `<sup>` | **yes** |
| A `<style>` block | **yes**, and its rules apply |
| `<svg>` with `<rect>`/`<text>`/`fill` | **yes** — painted, with real colours and sizes |
| `<img src="…">` (also `![…](…)` in Markdown) | **yes**, any origin |
| `[text](https://…)` and `[text](/path)` | **yes**, both relative and absolute |
| `<video>`, `<audio>`, `<canvas>` | kept as elements (useful, easy to misuse) |
| `<script>`, `<iframe>`, `onerror=`/`onclick=` and other handlers | **REMOVED** — do not plan around one |
| `href="javascript:…"` | the attribute is stripped |

Two consequences:

- **A page of HTML for a small result is the wrong trade.** The answer lives in a transcript that
  is read, searched and copied as one document; a `<div>` report inside it is readable, but it
  cannot be clicked through, opened in a tab, or kept. Reach for HTML/SVG when a picture says it
  faster than a sentence, not as a container for the whole answer.
- **The dangerous half is genuinely removed.** An answer is untrusted input to this renderer, so
  nothing that could execute is kept. Do not write a report that depends on JavaScript running —
  it will arrive as a dead tag.

## Pitfalls already paid for

- **The TUI does not render Markdown at all.** In a terminal your table arrives as pipe
  characters, your `##` as hashes, and your diagram as its own source. Prefer plain aligned text
  whenever you do not know the surface — the web interface renders plain text perfectly well too,
  so plain text is the choice that cannot lose.
- **A mermaid syntax error is shown as its own reason plus the source**, not as a red icon, so a
  bad diagram is visibly bad rather than silently empty. Check the syntax: `gitGraph` is picky
  (do not `branch` the branch you are already on — it answers *Trying to create an existing
  branch*), and a `gantt` needs its `dateFormat` before the tasks.
- **`-->` inside a diagram is not a problem, but it is the reason diagrams travelled as text.**
  That is history inside the renderer; write the arrow normally.
- **A diagram is not a substitute for the sentence.** A reader on a terminal, or someone reading
  a copied answer, gets the source. Say what it means.
