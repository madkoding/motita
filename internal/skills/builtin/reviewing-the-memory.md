# Reviewing the person's memory

Use when the person asks to check, tidy or fact-check their memory: which entries are out of date,
which contradict each other, which claims have no source, which entries are orphans or point
nowhere. It builds on `using-the-memory`; read that first for the tools and the rules about writing.

## Method

1. **Choose the scope.** A topic, a collection or the whole memory. For a topic, `recall in: memory`
   with its words and a few synonyms; for a collection, add `ns:Name`; for the whole memory, search
   the main subjects the person writes about.
2. **Read every entry in scope** with `recall` and `open`. Its "Links to" line shows links with no entry
   yet ("no entry yet"), and "Linked from" shows who depends on it. An entry nobody links to and
   that links to nothing is an orphan.
3. **Compare claims.** Put the same fact side by side across entries: dates, amounts, names, plans,
   decisions. Two different answers are a contradiction; a plan whose date has passed, or an entry
   that says "pending" next to another that says "done", is stale. The date and author shown with a
   search answer say which is the newer and who to ask.
4. **Check what can be checked.** A claim about the person's own project or machine can be
   verified with the tools of the mode (read the file, run the read-only command). A claim about
   the outside world needs a fetch, which plan mode does not have: say it is unverified.
5. **Report, do not rewrite.** Give a short list: each item names the entries as `[[Title]]` with
   their paths, quotes the conflicting lines and says what you checked. Separate "contradicts",
   "looks stale", "no source", "broken link" and "orphan".

## Fixing

- Only after the person agrees. Use `remember` to add a dated correction or a pointer to the
  entry that is right, or to create the missing entry a link points at.
- Never delete or overwrite: the old line stays, and the new one says what replaces it and why.
- Say what you changed and where, one line per entry.
