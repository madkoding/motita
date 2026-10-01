# Verifying a change: measure the effect, not the action

Use when you have changed something and have to say whether it worked: a fix, a build, a
configuration, a layout, a dependency bump. The failure this prevents is the expensive one — a
confident answer resting on "the command exited 0", which is a statement about the program
running and not about the effect existing.

## The rule that decides everything else

**`exit 0` is not the change.** `cp -r src dist` returning nothing says `cp` ran; it says nothing
about whether `dist` has anything in it. `npm run build` succeeding says the toolchain was happy;
it says nothing about the file the binary embeds. Every claim needs a SECOND read that observes
the result, and that read is what goes in the answer.

| The claim | Not evidence | Evidence |
| --- | --- | --- |
| "the tests pass" | the command exited 0 | the count, and the failing names if any |
| "it is fixed" | the code changed | the check that was red is green, and the same check was red before |
| "the feature works" | the code is there | the surface was driven and the measured value is right |
| "it is 15% faster" | the change looks faster | both numbers, same machine, same input, before and after |
| "the page shows it" | the CSS rule exists | the computed style / the pixel |

## New code ships with its own proof

If you added or changed behaviour — a component, a function, a handler, a flag — the task is not
finished when the code is written. Leave a test in the repository, in the same change:

1. Find how the project tests (neighbouring `*_test.*` files, the Makefile, `package.json`) and
   follow that style; do not invent a new harness.
2. Write a test that exercises the NEW code path and asserts on its result. For a fix, it must fail
   without the fix.
3. Run it and read the output for the test's NAME and count. `ok` with 0 tests run, or a suite that
   never imports your file, proves nothing.
4. Run the project's full gate too (build, lint, tests): a new test can pass while the change
   breaks a neighbour.
5. Report the test's path and its result. If the project genuinely cannot test this kind of change,
   say so and verify the effect another way (run it, render it, call it).

"The existing tests still pass" is not this: they never saw your code.

## Reproduce the failure BEFORE believing the fix

A fix you never saw fail is a fix you cannot claim. Three steps, in this order:

1. **Make the check bite.** Reintroduce the defect (undo the guard, put the wrong value back) and
   confirm the check now fails — its message, not just a non-zero status. A check that passes
   both with and without the fix asserts nothing, and this is the single most common way a test
   suite goes quiet.
2. **Restore it** and confirm it passes again.
3. **Say both runs out loud** in the answer: "red without it (`<message>`), green with it".

The same discipline applies to a fix for a rare failure: a green run after the change is not
evidence unless you have seen the failure first, under the SAME conditions (the same load, the
same flags, the same repeated runs). "It passed 12 times" and "it failed 2 times in 72 runs" are
different claims about the same code.

## Watch for the fix that passes for the wrong reason

A check can be green because it never reached the line it was written to protect. Before trusting
one:

- **Which line did it actually execute?** A fixture that breaks the STORE makes a handler fail in
  the LOOKUP before its own error branch runs — the test sees the status it expected, from the
  wrong place. Ask of every fixture "which return is this reaching".
- **Did the harness really run the named case?** A test filter with a typo, or a name that was
  renamed, exits 0 having run nothing and prints `ok`. Every "green" in a parameterised run is a
  suspect until you have seen the case count.
- **Did the anchor still match?** In a sweep that patches text and re-runs, a pattern whose text
  has already gone silently no-ops and the run reports success for a mutation it never applied.
  Print a skip when the anchor is missing, and count the skips.
- **Was the assertion on a class name or a computed value?** A rule can exist in the markup and
  emit nothing: this project has shipped Tailwind utilities that were in the class attribute and
  in the stylesheet and produced no CSS at all. Assert the rendered value.

## Say the number you measured, and its denominator

- A count without its scope is a guess: "0 failures" means nothing without "in 40 runs, with
  `-race`, under load".
- **Quote every sample, including the ones that came back clean.** A rare failure that appeared
  twice in 72 runs, with one 20-run sample clean, is not honesty to trim — publishing the table
  is what lets someone else reproduce you. Picking the favourable sample is how a report becomes
  a lie.
- **Re-measure a claim before repeating it.** A number in a doc, a comment or a previous answer
  was true for the tree it was written against. Run the command again; it is cheaper than
  defending a stale figure.
- **The same number twice is not a coincidence to skip.** If a file's hash, size or line count
  moved between the measurement and the delivery, measure the thing you are shipping.

## When the surface is a person, not a test

If the change is something a user SEES (a layout, a colour, a message, a flow), no assertion
replaces looking at it — and no screenshot replaces measuring it. Do both:

- **Screenshot** to catch what no assertion was written for. A screenshot caught a layout defect
  here AFTER every assertion had passed.
- **Geometry and computed styles** to prove it: `getBoundingClientRect`, `getComputedStyle`,
  pixel counts. "Centred" by a class is not centred when an animation also sets `transform`, and
  the difference is a number.
- **Both viewport sizes** if it is a layout: a defect that only appears at 390px wide is invisible
  on a desktop.

## Pitfalls already paid for

- **Verify against the artifact the user is RUNNING.** A rebuild, an install and a restart are
  three steps and stopping after the second is the classic "but I fixed it": the running process
  serves the OLD build, with the same port and the same answers. Read the version the live thing
  reports and compare it to the one you just built.
- **A stale cache reads exactly like a defect.** The browser's service worker, a compiled asset, a
  lockfile: all three serve the previous state and make a correct change look like it did nothing.
  Clear the cache before concluding the code is wrong.
- **Do not re-verify by re-reading the source you just wrote.** It agrees with itself. Verify the
  EFFECT: the served file, the running version, the response body, the measured value.
- **A verifier must not fail on the data it is given.** "No paused task found" is a property of
  the machine, not of the build. Print a skip and keep the verdict honest about what it saw.
- **Prove a snapshot restores before trusting it.** A backup that lists every ref and verifies as
  self-consistent can still be stale; the only proof is restoring it and comparing content.
