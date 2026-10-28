# Using the person's memory

Use when the person asks about their own plans, preferences, trips, people, decisions or anything
they may have written down, when you are about to state a fact about them, or when something worth
keeping comes up: a decision, a preference, a result. The memory is a folder of plain Markdown files
the person can also open in Obsidian, so what you write there is theirs.

## First: whether you have the memory at all

The tools exist only when the session has a memory. Plan mode lists `search_memory`, `read_memory`,
`write_memory` and `append_memory`; task mode takes the actions `memory_search`, `memory_read`,
`memory_write` and `memory_append`. If none is offered, say the memory is not reachable here and do
not answer from recall as if you had read it.

## How the memory is organised

- **Collections.** The first folder of an entry is its collection (a namespace): "Engineering",
  "HR", a project's name. Narrow a search to one with `ns:Name` (`ns:"Human Resources"` when it has
  a space) so another domain's entries do not crowd the answer.
- **Passages and sections.** A search matches small passages, each indexed with its entry's title
  and heading trail, and answers with the whole section the passage is in. The words you search
  need not be the words written: the memory also matches by meaning.
- **Who and when.** Entries may carry `author:` and `date:` properties. Filter with
  `author:Name`, `since:2026-01-31` and `until:2026-12-31` when the question is about a person or a
  period; the answer shows both next to each section.
- **Relations.** Entries link to each other with `[[Title]]`. For a question about how things
  relate (who depends on whom, what led to what) add `graph:1`: the entries linked from or to the
  best answers come back as "Related", each saying through which entry it was reached.

## Answering from the memory

1. **Search before you answer.** `search_memory` with the plain words of the question, plus the
   filters above when they apply. Try fewer or different words when nothing matches before saying
   there is nothing.
2. **Read the entry, not only the section.** `read_memory` takes a title or a path and also lists
   what it links to and what links to it. Follow a link that bears on the question.
3. **Cite where it came from.** Name the entry as `[[Title]]` with its path, so the person can open
   it. A fact with no entry behind it is your guess: say so.
4. **Say when entries disagree or look old.** Two entries with different answers, or a dated plan
   that has probably passed (look at the date shown), are worth reporting, not resolving silently.

## Writing to the memory

- **Write without being asked to.** Nothing is kept unless you write it. When a decision, a
  preference, a fact about the person's plans, people or setup, or a result worth keeping comes up,
  or they say "remember" or "note that", save it in the same turn: search for the entry that covers
  it, `append_memory` to it, or `write_memory` a new one. Do it before you answer, not "later".

- `write_memory` creates an entry and refuses to replace one. `append_memory` adds to the end of an
  existing entry, or creates it. Nothing here deletes or overwrites: to change a fact, append the
  new one and say what it replaces. A new entry is stamped with `author: motita` and today's date.
- Task mode takes `"entry :: the Markdown"`; the part before `::` is the title or path.
- Put the entry in the collection that fits: `Engineering/Rollback policy`, `HR/Hiring`. Write it
  with headings (`## Salary bands`), because a section is what a later search answers with. Start
  each passage with what it is about, so it makes sense read alone.
- Link related entries with `[[Title]]` so the graph and the backlinks connect them.
- Record only what the person said or decided, in their words. Never write secrets, passwords or
  tokens into the memory.
- Say what you wrote and where, in one line.
- To check, tidy or fact-check the memory as a whole, follow `reviewing-the-memory`.
