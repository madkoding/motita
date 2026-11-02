# Reviewing a design: fresh reviewers with no context, then resolve what they found

Use to review a design document, spec or RFC before implementing it: a goldfish review. The
author of a design (you, after "designing-a-change") shares every assumption in it, so the author
cannot see the gaps. Reviewers that start with NO conversation, only the document and the code it
cites, read it the way a new team member will, and the unstated assumptions surface. Works the
same on every model and provider: each reviewer is a `spawn_agent` with `"role": "review"`.

## What makes a reviewer a goldfish

- **Started fresh.** A background agent starts with no conversation history: only its brief. Keep
  it that way. The brief holds the document path, the line range, the lens and the output format;
  NEVER a summary of the discussion, the person's intent, or why a choice was made. If the
  reviewer needs a fact to understand the design, the document is missing it.
- **Read-only.** `"role": "review"` reads a copy of your current tree and changes nothing. Write
  the draft into the tree before you start reviewers; they cannot see what is only in the chat.
- **No fresh agents, no review.** If `spawn_agent` is refused (background agents turned off,
  `agent.max_parallel` is 0, or this mode has no such action), STOP and tell the person: a review
  run in your own context is not a goldfish review. Say they can turn background agents on.

The brief for one reviewer, as the `command` of the action:

```
Goldfish review: <lens> of docs/designs/<slug>.md lines <a>-<b>
Read the skill "design-review-lenses", section "<lens>", and follow its REVIEW instructions on
docs/designs/<slug>.md, lines <a>-<b>. Also read every file the document links or cites<, and
these for background: ...>. Report each finding with severity (Blocker, Major, Nit), the
affected section or lines, and the rationale. Report only findings; no praise.
```

## Step 1: Intake

1. **The document.** Ask for its path if you were not given one (an open question, not options).
2. **Extra reading** (optional). Reviewers always read the files the document cites; ask whether
   the person wants any other file added as background.
3. **The split.** Find the implementation heading (`## Detailed Implementation Plan`, or
   `Implementation Plan`, `Work Breakdown`, `Implementation Steps`) at line L: the FEATURE range is
   1..L-1 and the DETAILED range is L..end. Say which lines you found. With no such heading the
   whole file is the feature range and stage 5 is skipped; with several candidates, ask.
4. **The mode**, as ONE question: step by step with a checkpoint after each stage; all stages in
   one consolidated report; the feature spec only (stages 1-4); or the implementation plan only
   (stage 5).

## Step 2: The stages

Each stage starts its reviewers at once (up to `agent.max_parallel`; queue the rest), waits with
`wait_agents`, and writes the merged findings into `docs/designs/<slug>.review.md` under the
stage's heading. Lens names refer to sections of "design-review-lenses".

| Stage                   | Lenses                                            | Range          |
| ----------------------- | ------------------------------------------------- | -------------- |
| 1. Zoom out             | Zoom Out                                          | whole file     |
| 2. Comprehension        | Comprehension                                     | whole file     |
| 3. Design critique      | Design Critic, Structural, Style                  | feature range  |
| 4. Zoom out again       | Zoom Out                                          | whole file     |
| 5. Implementation ready | Implementation Readiness, Codebase Details, Style | detailed range |

The order matters: altitude and clarity first, details last. In step-by-step mode, end each
stage with ONE question: go to the next stage; resolve these findings with me (stages 3 and 5);
I will edit the document myself, tell me when to re-run; or pause. After the person edits, re-read
the file (the line numbers moved) and re-run that stage, ONCE: if they ask again after the
re-run, record what remains as accepted trade-offs and go on.

In consolidated mode run all five without stopping, then merge: deduplicate across stages,
group as 🔴 Blocker, 🟡 Major, 🟢 Nit, and keep a table that maps each merged item back to the
raw findings it came from.

## Step 3: Resolve the findings

You wrote the document, so a wish to contest a finding is not evidence that the reviewer is
wrong. Give each finding exactly ONE category:

- **PROPOSE FIX**: the critique is valid. Write the exact replacement text or deletion.
- **CONTEST**: the critique is wrong, and you can cite evidence the reviewer COULD have read: a
  file and symbol, or a decision written in the document. "This was intentional" is not evidence.
- **ESCALATE**: the critique is wrong, but the evidence is outside anything the reviewer could
  read (a team convention, a decision made aloud). Do not fix it; the person decides.
- **DECLINE**: a Nit not worth the edit. Record it.

Never contest a comprehension gap: if a fresh reader could not follow the document, it is
missing context. Never change a severity; contest it instead. Add context when a human reader
needs it, not only to satisfy a reviewer.

Write the categories, justifications and proposed edits into the review report, then tell the
person, with the ESCALATE items FIRST (they need the person's knowledge, not only approval), and
offer: accept all proposed fixes (contested, escalated and declined ones are skipped); go through
them one by one; continue without changes; or any other instruction. End the turn and wait. Apply
only what the person accepted, re-read the document to check it is still coherent markdown, and
mark the report `Status: resolved` with the counts of fixed, contested, escalated, declined and
open items.

## After the review

When the person approves, set the document's `Status: Approved`, commit it, and implement from
it. The review report is scratch; do not commit it unless the person wants it. A review does not
replace tests or the person's judgment.

---

Adapted from the Elephant-Goldfish Model (egm-review, egm-resolve, egm-review-loop,
https://github.com/google/elephant-goldfish-model), Copyright Google LLC, Apache License 2.0
(third_party/elephant-goldfish-model). Changed: merged and rewritten for motita's spawn_agent
review role and wait_agents, so it runs on any provider.
