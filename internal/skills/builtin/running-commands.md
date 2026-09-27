# Running commands and reading their output

Use when you are about to run something: to know BEFORE you write the line whether it will run,
ask, or be refused, and to know what the output you get back actually is. Two of the three
answers cost the user nothing; the third is the one that wastes a turn.

## The three verdicts, and how a line gets one

Every line is classified before it runs. The verdict is the WORST any segment earned, so one
`>` at the end of an otherwise harmless line is enough to change the answer.

| Verdict | What happens | What earns it |
| --- | --- | --- |
| **allow** | Runs, silently. | A known reader. A writer whose target is inside the workspace (`mkdir`, `touch`, `>` a log, `rm` a path in the project). The ordinary verbs of a project's own tooling. |
| **ask** | The user sees the exact line and decides. | A writer whose target is outside the workspace; a program that reaches the network or the system (`git push`, `curl`, `ssh`, `systemctl restart`, a package manager); anything the policy cannot classify. |
| **refuse** | Does not run. Do not retry it. | The mandatory floor: `mkfs.*`, `fdisk`/`parted`, `dd` with an `of=`, `shred`, `wipefs`, `shutdown`/`reboot`, and `rm`/`mv`/`cp`/`ln` aimed at a root or a home tree. |

A question is not a failure: it is one keystroke for the user. **A refusal is final** — no YAML key,
environment variable or flag relaxes it, so rephrasing only spends a second turn. Say what you
needed and why, and find another route.

## Never wrap a line in `sh -c`

`sh -c 'ls'` and `sh -c 'rm -rf /'` are the same shape to a classifier, so a shell line can only be
approved as a whole: it always costs a confirmation. **Run the program directly and it is judged on
its own merits.** That is the whole reason plan mode can be a guarantee rather than a request —
there, the program runs with no shell at all, so a pipe or a `$(...)` is not refused, it is
*impossible*.

## Plan mode is a different world: readers only

`agent.read_only` (plan mode) has no shell and no writers:

- Only a known **reader** runs. Pipes, redirections and `;` cannot be expressed at all.
- `sed` and `awk` are writers even for a read-shaped invocation (`sed -n` only reads, but `sed -i`
  rewrites, and one list is easier to keep honest than a table of exceptions). So are `python3`,
  `node`, `perl` and every other interpreter.
- `find` is a reader until `-delete`, `-exec`, `-execdir`, `-ok` or `-fprint*` appear.
- `git` is a reader only for a fixed set of verbs (see the git procedure); `git add`, `git commit`
  and `git worktree` are writers and are refused.
- `curl` and `wget` are refused **by name**, `w3m` and `lynx` are unclassified — so **there is no web
  access in plan mode at all**. A lookup that succeeds there is DNS (`dig`, `host`, `getent` are
  readers) and nothing more.

## Reading the output: it is cut, and it is cut twice

Two different caps exist and they are not the same thing:

- The command's output is captured up to a **sandbox limit** (`sandbox.max_output_kb`, 256 KiB). Past
  it the text stops and the result carries `[output truncated by the sandbox limit]`.
- What reaches the model is separately limited: a tool result past **64 KiB** is cut with
  `[... output truncated to N KiB ...]`.

**Neither cut lands at a record boundary.** It falls wherever the byte budget runs out, in the
middle of a line, so a truncated JSON document is not a JSON document — it is a prefix with a
banner. When the answer matters:

- **Ask for less.** `head -n 40`, `grep -n pattern`, `wc -l`, `du -sh`. A count is cheaper than the
  listing.
- **Write the big thing to a file and read the slice you need.** A file read is capped at 1 MiB, and
  a redirect to a path inside the workspace is ordinary work.
- **Never let a big document arrive whole** and then look for its tail: the tail is what was cut. If
  you need the end, ask for the end (`tail -n 40`) in its own call.

## Know the file before you read it

`cat` on something that is not text fills the context with bytes nobody can recover from, and the
cut makes it worse. `file path` first. For a peek: `xxd -l 256 path` or `strings path | head`.

## Reading a value out of structured text without a parser

`jq` is on the reader list but is **not installed on every machine**; `python3` is installed
essentially everywhere but it is a *writer* (refused in plan mode, and a hard **refuse** under
`agent.policy.strict`). So the tool that is both a reader and always present is `grep`:

```sh
grep -o '"name":"[^"]*"' answer.json      # one field, one per line
grep -n 'ERROR' build.log                 # the failing lines, with their numbers
```

`sort`, `uniq -c`, `cut`, `tr`, `wc` and `diff` are readers too, so a pipeline of readers works in
both modes' terms — but only task mode has a shell to run it in.

## A command that succeeded is not a change that happened

`exit 0` means the program ran, not that the effect is there. **Verify the effect with a reader** —
the file that should exist, the line that should be in it, the count that should have moved — and
report what you measured. `cp -r src dist` returning nothing is not evidence that `dist` has
anything in it.

## When a line is refused or declined

- **Read the reason.** It names the rule (`external-effect`, `writer-unclassified`, `shell-line`,
  `readonly`), which tells you what the classifier objected to.
- **Do not re-run a declined line, and do not route around it.** A no is a decision. Offer the
  narrower thing you actually need, or say plainly that the fetch or the push is required.
- **Do not build a path from a variable, a glob over the whole tree, or a command substitution.**
  `rm -rf "$X"` is a confirmed action on a path nobody can read, including you. List first, read the
  list, then delete what you saw — `rm -rf` with a wrong target is the one mistake with no undo.
- **A glob is expanded by the shell**, so `rm *.tmp` may arrive as the literal string. Let the
  program glob (`find . -name '*.tmp' -delete`), or list and act on real names.
