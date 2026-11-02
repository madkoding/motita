# Checking design drift: does the code still do what the design documents say?

Use when asked whether the code still matches its design documents, specs or READMEs, or to
detect drift after a feature landed: a report of every place where the code diverged from the
documents or never implemented them. The failure this prevents is a design document that keeps
being read as the truth long after the code went another way.

## Method

1. **The documents.** Use the folder you were given; otherwise the project's design folder
   (`docs/designs/` after "designing-a-change"). Ask only when neither exists.
2. **One document at a time**, in order, so no result is lost to a long output:
   - Read the document: its intent, the changes it proposes, every file, type, field, function,
     flag and endpoint it names.
   - Find the code. Paths it names first; otherwise search for the symbols it names. When you had
     to search, suggest adding the paths to the document so the next check is cheaper.
   - Read the CURRENT code, not your memory of it, and compare.
3. **What counts as drift:**
   - **Missing feature**: logic the document describes that the code does not have.
   - **Incomplete model**: a struct, type, schema or message missing a field the design specifies.
   - **Shortcut**: a hard-coded value where the design planned real logic.
   - **Divergent architecture**: a different pattern, library or flow than the one proposed.
   - **Undocumented logic**: behaviour in the code that the document claims to cover and does not
     mention.

   A name the document gives that the code does not have is drift. Double-check before saying
   anything matches.

4. **Report only the differences.** Do not describe what matches. Write one report,
   `docs/designs/<YYYY-MM-DD>-drift-report.md`, with one section per document:

   ```markdown
   ## <document name>

   - <each divergence: what the document says, what the code does, file:line>
   ```

   or `No drift detected.` for a document that matches. Tell the person the path and the worst
   2-3 divergences; fixing the code or the document is their call (an `Approved` design that the
   code outgrew may need a new revision rather than a revert).

## Doing it in parallel

For many documents, one `spawn_agent` with `"role": "research"` per few documents, each briefed
with ONLY the document paths and the method above, keeps each reader fresh; merge their reports
into the one file. For a handful of documents, do them yourself.

---

Adapted from the Elephant-Goldfish Model (egm-drift-report,
https://github.com/google/elephant-goldfish-model), Copyright Google LLC, Apache License 2.0
(third_party/elephant-goldfish-model). Changed: rewritten for motita's actions and paths.
