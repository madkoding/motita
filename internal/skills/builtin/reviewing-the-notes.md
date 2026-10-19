# Reviewing the person's notes

Use when the person asks to check, tidy or fact-check their notes: which are out of date, which
contradict each other, which claims have no source, which notes are orphans or point nowhere. It
builds on `using-the-notes`; read that first for the tools and the rules about writing.

## Method

1. **Choose the scope.** A topic, a folder or the whole vault. For a topic, `search_notes` with its
   words and a few synonyms; for the vault, search the main subjects the person writes about.
2. **Read every note in scope** with `read_note`. Its "Links to" line shows links with no note yet
   ("no note yet"), and "Linked from" shows who depends on it. A note nobody links to and that links
   to nothing is an orphan.
3. **Compare claims.** Put the same fact side by side across notes: dates, amounts, names, plans,
   decisions. Two different answers are a contradiction; a plan whose date has passed, or a note
   that says "pending" next to another that says "done", is stale.
4. **Check what can be checked.** A claim about the person's own project or machine can be
   verified with the tools of the mode (read the file, run the read-only command). A claim about
   the outside world needs a fetch, which plan mode does not have: say it is unverified.
5. **Report, do not rewrite.** Give a short list: each item names the notes as `[[Title]]` with
   their paths, quotes the conflicting lines and says what you checked. Separate "contradicts",
   "looks stale", "no source", "broken link" and "orphan".

## Fixing

- Only after the person agrees. Use `append_note` to add a dated correction or a pointer to the
  note that is right, and `write_note` to create the missing note a link points at.
- Never delete or overwrite: the old line stays, and the new one says what replaces it and why.
- Say what you changed and where, one line per note.
