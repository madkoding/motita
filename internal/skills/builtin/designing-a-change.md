# Designing a change: brainstorm, then write the design document before the code

Use when a feature or a larger change needs design decisions before anyone writes code: a new
subsystem, a data model, an API, a migration, anything with more than one defensible answer. You
are the ELEPHANT: you hold the person's context and write the design; fresh reviewers with none of
that context test it afterwards ("reviewing-a-design"). A small, clear fix does not need this:
use the normal coding workflow. The failure this prevents is the expensive one: code written on
an assumption nobody stated, found wrong after it shipped.

This works the same on every model and provider. It needs only files and, for the review, the
`spawn_agent` action with `"role": "review"`.

## Rules that hold in every step

- **Files, not chat.** The brief and the design go into files; the answer to the person names
  the file and highlights 2-3 decisions or risks. Never paste a whole draft into the chat.
- **Plain English.** Explain the mechanism directly. No promotional adjectives (_seamless_,
  _robust_, _blazingly fast_) and no decorative adverbs (_simply_, _effortlessly_, _obviously_).
- **Stress-test, do not agree.** Lead with what is wrong, missing or risky. For each proposal ask:
  what is the person not seeing, what is the strongest counter-argument? Do not call an idea
  "great" or "smart"; agree only when it has survived scrutiny. When corrected, state the
  corrected logic instead of apologising.
- **The person decides the architecture.** When a decision has more than one defensible answer
  (data model, component boundaries, storage, protocol), give up to three options, each with how
  it works, its trade-offs and when it fails, and ask the person to choose. Do not rank them or
  pick one; if asked to "just pick", restate the trade-offs and ask again. Decide mechanical
  choices (file names, formatting) yourself.
- **One question at a time.** A checkpoint ends your turn with ONE question; wait for the answer.

## Where the files go

Use the project's design folder if it has one; otherwise `docs/designs/`. For a design named
`<slug>` (lowercase, hyphenated):

| File                            | What it is                                                       |
| ------------------------------- | ---------------------------------------------------------------- |
| `docs/designs/<slug>.brief.md`  | the concept brief from the brainstorm (step 1)                   |
| `docs/designs/<slug>.md`        | the design document; `Status: Draft` until the person approves   |
| `docs/designs/<slug>.review.md` | the review report ("reviewing-a-design"); scratch, not committed |

The reviewers read a copy of your working tree, so the files must be written there, not only
described.

## Step 1: Brainstorm the concept

1. **Drill into the problem**, over as many turns as it takes, with open questions (no canned
   multiple-choice answers): what problem or pain does this solve; who or what uses it and under
   which guarantees; the hard constraints (offline, zero false positives, a platform); the edge
   cases and what is out of scope; how it is handled today and why that does not work; how
   success will be measured.
2. **Anchor the hypothesis.** When the problem is clear, state it in 2-3 sentences and ask ONE
   question: review it with fresh reviewers now, or clarify more? Do not start reviewers before the
   person says go.
3. **Fresh-eyes brainstorm.** Start one `spawn_agent` with `"role": "review"` per brainstorm lens
   in "design-review-lenses" (Contrarian/YAGNI, cost and quotas, ergonomics, migration,
   observability, long-term maintenance), all at once so none biases another. Each brief holds
   ONLY the hypothesis text and its lens; never the conversation. Then `wait_agents`.
4. **Write the brief** to `<slug>.brief.md`: deduplicate the findings and keep this layout:

   ```markdown
   # <Feature name>

   - **Author:** <name> · **Date:** <YYYY-MM-DD>

   ## Overview

   ## Analysis (what the lenses found, merged, in plain English)

   ## Goals vs. Non-Goals

   ## Alternatives Weighed (every option rejected, with the reason: the draft has no other source)

   ## Key Decisions (what was settled, to carry into the draft)
   ```

5. **Iterate.** Name the file and the 2-3 strongest objections, and ask how to address them
   (pivot, narrow the scope, add a constraint). Repeat until the person is satisfied, then ask
   ONE question: draft the design now, publish the brief as an RFC for the team, keep refining,
   or stop here (the brief stays saved).

## Step 2: Draft the design section by section

Start from the brief (or the person's notes when there is none; ask for the problem, goals and
context if neither exists) and confirm with the person that it is the right starting point.
Write each section into `<slug>.md`, then end the turn naming the section, its 2-3 key decisions
and the file, and ask ONE question: approve it and go on, or revise it. Do not move on without
the approval; the person may go back to an earlier section at any time.

1. **Overview, The Problem, Goals, Non-Goals.** The _why_ (the need and its value), not the how.
   Goals are measurable; non-goals draw the boundary that stops scope creep.
2. **Technical Plan.** New components, changed files, API changes, data flow; a text diagram
   (ASCII or Mermaid) where a picture helps (see "diagrams-and-reports").
3. **Alternatives Considered.** Copy them from the brief's "Alternatives Weighed" (drafting often
   happens in a new session: do not rely on memory). For each: pros, cons, why it was rejected.
4. **Detailed Implementation Plan.** Detailed enough that someone could implement it without
   making an impactful decision. List EVERY file to create, modify or delete (code, tests, docs,
   config, schemas), each marked Create/Modify/Delete with what changes and why. No real code:
   pseudocode, signatures and schemas are fine. End with the verification plan: build, tests,
   lint, the coverage the project requires.

Keep unresolved questions under **Open Questions** so a later session does not ask them again,
and remove each one when it is answered.

### The design document layout

```markdown
# <Feature name>

- **Author:** <name> · **Status:** Draft | In Review | Approved · **Date:** <YYYY-MM-DD>

## Overview

## The Problem

## Goals

## Non-Goals

## Open Questions

## Critical User Journeys (Given / When / Then for each)

## Technical Plan

## Alternatives Considered (Pros / Cons / Reason for rejection, per alternative)

## Non-Functional Requirements (security, performance, rollout and rollback; only the critical ones)

## Detailed Implementation Plan

### Target files to create or modify

### Verification plan
```

## Step 3: Reconcile, then review

Before calling the draft finished, re-read the brief and compare: every **contradiction** (the
brief decided a question one way, the draft another) and every **addition** that changes the
architecture or an external interface goes to the person. No reviewer can do this check, because
none of them reads the brief. Then run "reviewing-a-design" on `<slug>.md`. When the person
approves, set `Status: Approved`, commit the design (not the review report) and implement from
it; after the code lands, "checking-design-drift" keeps the two honest.

---

Adapted from the Elephant-Goldfish Model (egm-brainstorm, egm-draft,
https://github.com/google/elephant-goldfish-model), Copyright Google LLC, Apache License 2.0
(third_party/elephant-goldfish-model). Changed: merged into one procedure, rewritten for motita's
actions (spawn_agent, wait_agents) so it runs on any provider.
