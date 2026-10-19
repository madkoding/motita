# Using the person's notes

Use when the person asks about their own plans, preferences, trips, people, decisions or anything
they may have written down, when you are about to state a fact about them, or when something worth
keeping comes up: a decision, a preference, a result. The notes are a vault of plain Markdown files
the person can also open in Obsidian, so what you write there is theirs.

## First: whether you have the notes at all

The tools exist only when the session has a vault. Plan mode lists `search_notes`, `read_note`,
`write_note` and `append_note`; task mode takes the actions `note_search`, `note_read`,
`note_write` and `note_append`. If none is offered, say the notes are not reachable here and do not
answer from memory as if you had read them.

## Answering from the notes

1. **Search before you answer.** `search_notes` with the plain words of the question. Try fewer or
   different words when nothing matches before saying there is nothing.
2. **Read the note, not only the snippet.** `read_note` takes a title or a path and also lists what
   it links to and what links to it. Follow a link that bears on the question.
3. **Cite where it came from.** Name the note as `[[Title]]` with its path, so the person can open
   it. A fact with no note behind it is your guess: say so.
4. **Say when notes disagree or look old.** Two notes with different answers, or a dated plan that
   has probably passed, are worth reporting, not resolving silently.

## Knowing a project before you change it

When the work is in a project, check whether the scan already mapped it before reading the code
cold: `read_note` `Projects/<project> - Architecture`, or `search_notes` "Architecture of" with the
project's name. It lists the modules, which one imports which and, when the person turned the
model option on, how the project fits together and its main flows. Use it to decide where to look,
then confirm in the code: the note is a map made at the last scan, not the code itself. If it is
missing, work from the code as usual; do not ask the person to scan first. If what you find in the
code contradicts the note, say so and offer to `append_note` the correction.

## Writing to the notes

- `write_note` creates a note and refuses to replace one. `append_note` adds to the end of an
  existing note, or creates it. Nothing here deletes or overwrites: to change a fact, append the
  new one and say what it replaces.
- Task mode takes `"note :: the Markdown"`; the part before `::` is the title or path.
- Link related notes with `[[Title]]` so the graph and the backlinks connect them. Put a note in a
  folder when the person has one for it (a project folder, a topic).
- Record only what the person said or decided, in their words. Never write secrets, passwords or
  tokens into a note.
- Say what you wrote and where, in one line.
- To check, tidy or fact-check the notes as a whole, follow `reviewing-the-notes`.
