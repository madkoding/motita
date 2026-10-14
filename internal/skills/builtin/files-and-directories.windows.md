# Files and directories

Use when reading, searching, creating, moving, copying or deleting files and folders: locating
a file, finding where something is defined, inspecting a tree, cleaning a directory, or editing
many files at once. This is the Windows version: commands run under PowerShell, not sh.

## First: which mode you are in, because the tool names differ

The same library is served to both modes, so this document has to say what exists where.

| Mode                 | How you act  | Names                                                               |
| -------------------- | ------------ | ------------------------------------------------------------------- |
| **Plan** (read-only) | tool calls   | `list_directory`, `read_file`, `search_in_files`, `execute_command` |
| **Task** (writes)    | JSON actions | `{"kind":"command","command":"..."}`, plus the four skill kinds     |

In **plan mode, use the three file tools** — `list_directory`, `read_file` and `search_in_files`
work the same on Windows (the search runs inside the program, no grep needed). `execute_command`
runs a program directly with no shell, so a cmdlet such as `Get-Content` does not exist there:
only real programs (`git`, `findstr`, `tree`) run, and only the ones known to read.

In **task mode there is no `read_file`**. A `kind` of `read_file` is refused. Read with a
command instead, written for PowerShell.

## Reading and finding

| Goal                  | Command                                                  | Note                                         |
| --------------------- | -------------------------------------------------------- | -------------------------------------------- |
| List a directory      | `Get-ChildItem -Force dir`                               | `-Force` shows hidden entries                |
| Read a file           | `Get-Content -Raw file`                                  | check it is text first (below)               |
| A slice of a big file | `Get-Content -TotalCount 40 f` / `Get-Content -Tail 40 f` | never pull 100 MB in to read 10 lines        |
| Find text in files    | `Select-String -Pattern main -Path src\*.go`             | prints `file:line:text`                      |
| Find text in a tree   | `git grep -n pattern` (in a repository)                  | or `findstr /S /N /C:text *.go`              |
| Find files by name    | `Get-ChildItem -Recurse -Filter *.go src`                | add `-Name` for bare paths                   |
| Where a name is used  | `git grep -n symbol`                                     | recursive, skips ignored files               |

Search first, then read only what matched. Reading each candidate in turn is how a context fills
with files that were never relevant.

**Do not write the Unix forms.** `ls -la`, `head -n 40`, `grep -rn`, `2>/dev/null` and `rm -rf`
fail under PowerShell (its `ls` and `cat` are aliases of the cmdlets and take their parameters,
not Unix flags). Write the cmdlet names in full: aliases differ between PowerShell versions.

## Know the file before you read it

- `Get-Item file | Select-Object *` — size, attributes, link target, timestamps
- `Format-Hex -Count 64 file` — the first bytes, to tell text from binary
- `Get-ChildItem -Force dir` — sizes and hidden entries (a plain listing hides `.git`, `.env*`)
- `Get-Item f` (its `Length`) or `Get-Content f | Measure-Object -Line` — how big it is, before
  pulling it into context
- `Resolve-Path path` — the full path; `Test-Path path` — whether it exists

## What the guardrails do, so a refusal is not a surprise

- **Reads are silent.** `Get-Content`, `Get-ChildItem`, `Get-Item`, `Test-Path`, `Resolve-Path`,
  `Select-String`, `Measure-Object`, `Format-Hex`, `findstr` and the Unix-named readers run
  without asking.
- **Everything else is confirmed.** The guardrails cannot yet check Windows paths, so a write is
  asked about even inside the workspace: `New-Item`, `Set-Content`, `Copy-Item`, `Move-Item`,
  `Remove-Item`, a `>` redirection. The user sees the line and decides; a no is a decision, not an
  error to retry.
- **Some syntax makes a whole line unreadable, and it is confirmed as a whole**: a script block
  (`{ }`), a `$variable` or `$(...)`, `@(...)`, parentheses, a backtick, `#`, a `[Type]` literal, a
  network path (`\\host\share`), and a backslash before anything but a path character. A read
  written with one of these stops to ask; the same read with literal arguments does not.
- **Never wrap a command in `powershell -Command` or `cmd /c`.** A shell line cannot be checked,
  so it always costs the user a confirmation.

Practical consequences:

- Put a path with spaces in **single quotes**: `Get-Content 'C:\My Project\notes.txt'`. In single
  quotes nothing is expanded, so the guardrails read what PowerShell reads.
- Prefer a parameter to a pipeline into `Where-Object { ... }` or `ForEach-Object { ... }`:
  `Get-ChildItem -Recurse -Filter *.log` instead of filtering in a script block.
- Use `$null` only when you accept a confirmation; to drop errors on a read, prefer
  `-ErrorAction SilentlyContinue`.

## Working patterns

**Find, then act.** `Select-String` or `git grep -n` for the paths, `Get-Content` only on what
matched.

**Move or copy a whole tree** — one call, so the user is confirming the thing you actually mean:

```
Copy-Item -Recurse src\assets dist\assets
Move-Item old new
```

**Delete carefully.** `Remove-Item -Recurse build` is a confirmed action on a path you named.
List first (`Get-ChildItem -Recurse -Filter *.tmp`), check the list, then delete what you saw.
Never build the path from a variable or a wildcard over the whole tree — a recursive delete with
a wrong target has no undo.

**Create a tree**: `New-Item -ItemType Directory -Force a\b\c` (one call). Create a file with
`New-Item -ItemType File path` or write it with `Set-Content -Path f -Value 'text'`.

**Edit in place**: `Set-Content` with the whole new text, or one scripted pass for a mechanical
edit across many files — it is worth one confirmation rather than N.

## Pitfalls already paid for

- **Encoding.** Windows PowerShell 5.1 writes UTF-16 with `>` and `Out-File`, and many tools then
  read garbage. Write with `Set-Content -Encoding utf8` (PowerShell 7 writes UTF-8 by default).
- **Line endings.** Files written on Windows get `CRLF`; a repository that expects `LF` shows a
  diff on every line. Check `git config core.autocrlf` before rewriting a file wholesale.
- **A huge file will not be read whole.** `-TotalCount` and `-Tail` read a slice;
  `Select-String` finds the region to read.
- **Binary in the context is unrecoverable.** `Format-Hex -Count 64` first.
- **Hidden files are most of the bugs**: `Get-ChildItem -Force`.
- **A path is case-insensitive but a repository is not**: `Readme.md` and `README.md` are the same
  file to Windows and two files to git on Linux.
- **A link is not the file.** `Remove-Item` on a junction or symlink removes the link; check with
  `Get-Item path | Select-Object LinkType, Target` before deleting anything that might be one.
- **Do not re-run a declined command or route around it.** Choose a different approach the user
  can see, or say plainly that what you need was refused.

## When the work is done, say what changed

Report the paths touched and what happened to each — created, edited, moved, deleted — from what
the commands actually returned. "Cleaned up the build directory" is not reportable; "removed
`build\` (7 files, 12 MB)" is.
