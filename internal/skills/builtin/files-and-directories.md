# Files and directories

Use when reading, searching, creating, moving, copying or deleting files and folders: locating
a file, finding where something is defined, inspecting a tree, cleaning a directory, or editing
many files at once.

## The tools, and their real limits

| Goal | Tool | Limit that bites |
| --- | --- | --- |
| List one directory | `list_directory` | one level, names only, no sizes or modes |
| Read a file | `read_file` | text only, 1 MiB max, errors on a directory |
| Find text in files | `search_in_files` | regex (default) or literal, under a directory |
| Run a reader | `execute_command` | ONE command, no pipe, no redirect, no `;`/`&&` |
| Anything that writes | `command` action | the policy decides: silent, confirm, or refuse |

`search_in_files` is the cheapest lookup there is. Search for the symbol or the string, not the
file name: one call beats `ls`, then `cat`, then `grep`. When you already know the path, `grep -n`
through `execute_command` gives line numbers in one shot.

Reading a slice of a big file: `head -n 40 file`, `tail -n 40 file`, `head -c 4096 file` are all
readers and cost nothing. Do not `read_file` a file you only need a few lines from.

## Know the file before you read it

- `file path` — is it text, binary, a symlink, a directory?
- `ls -la dir` — sizes, modes, symlink targets, hidden entries (a bare `ls` hides all of these)
- `stat path`, `readlink -f path`, `realpath path` — where a link really points
- `wc -l file`, `du -sh dir`, `df -h` — how big anything is, before pulling it into context

A directory where `read_file` should have worked means you skipped this step.

## What the guardrails do, so a refusal is not a surprise

Two layers decide, and only the first one is negotiable:

- **The floor — refused always.** No setting in any configuration file relaxes it. Formatting or
  partitioning a disk (`mkfs.*`, `fdisk`, `parted`), `dd` with an `of=`, `shred`, `wipefs`,
  `shutdown`/`reboot`, and `rm -rf` or `mv`/`cp`/`ln` aimed at a root or a home tree.
  There is no phrasing that gets these through, so do not spend a call trying.
- **Confirm — runs only after the user says yes.** A writer whose target is outside the workspace,
  a program that reaches the network or the system (`git push`, `npm publish`, `ssh`, `curl`,
  `systemctl restart`, a package manager), and anything the policy cannot classify. The user sees
  the line and decides; a no is a decision, not an error to retry.
- **Silent — ordinary work.** Readers, and a writer whose target is inside the workspace you were
  pointed at: `mkdir`, `touch`, `>` a log, `rm` a path in the project. This is what writing looks
  like and it does not ask.

Practical consequences:

- **Never wrap a command in `sh -c '...'`.** A shell line cannot be checked — `sh -c 'ls'` and
  `sh -c 'rm -rf /'` look identical — so it always costs the user a confirmation. Run the program
  directly and it is classified on its merits.
- In **plan mode** only known readers run: no pipes, no redirections, no chaining. Unknown
  programs are refused, not asked about. Compose the answer from several reader calls instead.
- `sed` and `awk` write, so both are treated as writers even for a read-shaped invocation.
  `grep`, `cut`, `sort`, `uniq`, `tr`, `jq` are the reading equivalents.
- `find` is a reader until `-delete`, `-exec`, `-execdir` or `-fprint*` appear, and then it is a
  writer (or a refusal). Prefer `find dir -name '*.go'` plus a separate call for the action.
- `git` is read-only only for `status`, `log`, `diff`, `show`, `branch`, `remote`, `tag`,
  `blame`, `ls-files`, `cat-file`, `grep`, `rev-parse`, `shortlog`, `reflog`. Everything else
  changes the repository and is confirmed.

## Working patterns

**Find, then act.** `search_in_files` to get the paths, `read_file` only on what matched. Reading
each candidate in turn is how a context fills up with files that were never relevant.

**Move or copy a whole tree** — one call, with the target inside the workspace, so the user is
confirming the thing you actually mean:

```
cp -r src/assets dist/assets      # silent: inside the workspace
mv old/ new/                      # confirmed if either side leaves the workspace
```

**Delete carefully.** `rm -rf build` is a confirmed action on a path you named. Never build the
path from a variable, a glob over the whole tree, or a `find` you have not read the output of
first — `rm -rf` with a wrong target is the one mistake here that has no undo. List first
(`find . -name '*.tmp'`), check the list, then delete what you saw.

**Empty a directory but keep it:** `find dir -type f -delete` is refused (a writer's target is not
in its arguments); use `rm -r dir` and `mkdir dir`, or `rm dir/*` after listing the matches.

**Edit in place** (task mode): write the file with a `command` action, or `python3 -c` /
`sed -i` — both are writers and both confirm. For a mechanical edit across many files, it is worth
one confirmation to do it in a single scripted pass than N confirmations for N files.

**Create a tree** the shell would do in one line: `mkdir -p a/b/c` (one call, `-p` is fine), and
`touch`, `tee`, or a redirection to create a file. Redirections are judged by their target — a log
inside the workspace is work, `> /etc/hosts` is not.

## Pitfalls already paid for

- **A glob is expanded by the shell the user is not running.** Commands here are executed
  directly, so `rm *.tmp` may arrive as the literal string. List with `find` or `ls` first and act
  on real names, or let the program glob (`find . -name '*.tmp' -delete`).
- **`read_file` on a 1 MiB+ file fails by design.** Use `head -c`/`tail -c`, `wc -l`, or `grep -n`
  to work a slice, then read the region you need.
- **Binary in the context is unrecoverable.** `file` first; `xxd -l 256` or `strings file | head`
  for a peek. A pulled-in binary burns the window and tells you nothing.
- **Hidden files are most of the bugs** in "why is this not building": `ls -la`, and remember
  `.gitignore`, `.env*` and dot-directories are not in a plain listing.
- **`find` prints paths relative to where you started.** Run it from the directory you were
  pointed at, and quote the pattern so the shell does not eat it.
- **A symlink is not the file.** `rm link` removes the link, `rm -r link/` follows it and deletes
  the target. Check with `ls -l` before deleting anything that might be one.
- **Do not re-run a declined command or route around it.** Choose a different approach the user
  can see, or say plainly that what you need was refused.

## When the work is done, say what changed

Report the paths touched and what happened to each — created, edited, moved, deleted — from what
the commands actually returned. "Cleaned up the build directory" is not reportable; "removed
`build/` (7 files, 12 MB)" is.
