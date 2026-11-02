# Elephant-Goldfish Model (third-party)

- Upstream: https://github.com/google/elephant-goldfish-model
- Revision adapted: `36343a16f0e0d830a7ed78b0da97695379ab3243`
- Copyright Google LLC, licensed under the Apache License 2.0 (see `LICENSE` in this folder).
  Upstream states it is not an officially supported Google product.

## What motita uses

These built-in procedures in `internal/skills/builtin/` are adapted from EGM's skills and reviewer
personas:

| motita procedure        | Adapted from                                          |
| ----------------------- | ----------------------------------------------------- |
| `designing-a-change`    | `skills/egm-brainstorm`, `skills/egm-draft`           |
| `reviewing-a-design`    | `skills/egm-review`, `egm-resolve`, `egm-review-loop` |
| `design-review-lenses`  | `agents/*.md` (the goldfish reviewer personas)        |
| `checking-design-drift` | `skills/egm-drift-report`                             |

## Changes made

The text was condensed and rewritten so it runs inside motita on any model or provider: the
fresh-context reviewers are motita background agents (`spawn_agent` with `"role": "review"`,
then `wait_agents`) instead of a harness-specific subagent tool; the persona files are one
procedure a review agent reads by lens; the scratch files live under `docs/designs/`. No upstream
code is included. Each adapted procedure ends with its attribution.
