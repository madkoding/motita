# Pull requests and CI: open one, link it, and see it through to green

Use when work in a repository is ready to be proposed: opening a pull request (a merge request on
GitLab), telling the user where it is, and making sure its CI passes. The failure this prevents is
the common one — "I opened a PR" with no way to reach it, and a red check nobody looked at.

## Everything is semantic

Commit subjects AND pull request titles follow Conventional Commits: `type(scope): description`.

| Type | Use for |
| --- | --- |
| `feat` | a new capability the user can see |
| `fix` | a bug fixed |
| `docs`, `style`, `test` | documentation, formatting only, tests only |
| `refactor`, `perf` | behaviour-preserving restructuring, speed |
| `build`, `ci`, `chore` | build system, pipelines, everything else |
| `revert` | undoing an earlier commit |

`!` after the type or scope marks a breaking change (`feat(api)!: drop v1`). The subject is one
line under 100 characters, in the imperative, saying what the change DOES. A commit whose `-m`
message is not semantic is **refused** before it is made, and `motita forge pr create` refuses a
title that is not; the refusal names the form. Put the detail in the body, not in the subject.

## Opening the pull request

1. The work is committed on a branch that is NOT the base branch, and the branch is pushed:
   `git push -u origin <branch>` (the user is asked, as for every push).
2. Open it with the command motita gives you — never with curl, and never by asking the user for
   a token. The login the user made in motita's settings is used for you:

```sh
motita forge pr create --title "feat(auth): connect to GitHub" --body-file - <<'BODY'
## What
...
## Why
...
## How it was verified
...
BODY
```

   It works for GitHub, GitLab, Bitbucket and Gitea alike, reads the host from `origin`, defaults
   to the repository's default branch as the base (`--base` overrides), and returns the existing
   pull request instead of opening a second when the branch already has one. `--draft` opens a
   draft. The user is asked before it runs.
3. **Always give the user the link.** The command prints `Pull request #12 opened: <url>` and a
   line `Cite it as: [owner/repo#12](<url>)`. Repeat that markdown link in your answer, with the
   number inside the link text. A bare `#12` is not enough: the user must be able to click through.

If it says the host is not connected, tell the user to connect it (Settings → Git connections in
the web interface, or `/git connect` in the terminal) and try again afterwards. Do not look for a
token, do not try `gh`, and do not edit git configuration to get around it.

## CI: offer to watch it, then watch it

After the pull request exists, **offer to follow its CI** ("Want me to watch the CI and fix it if
it fails?"). If the user agrees, or the task already says to get it merged:

```sh
motita forge pr checks --wait --logs      # blocks until the CI ends; --timeout 45m to change the limit
```

Exit status: `0` the CI passed, `1` it failed, `3` it is still running or there is no CI at all.
The output lists every job with its state; `--logs` adds the end of the log of each failed job
(GitHub), and the failed job's URL is printed for every host.

When it fails:

1. Read the failing job's log. Reproduce the failure locally with the project's own check before
   changing anything — a fix you did not see fail first is a guess.
2. Fix the cause in code the pull request touches. **Never** skip, disable or delete a test to get
   green, and never re-run a job hoping it passes: a failure that comes back is real.
3. Commit with a semantic message (`fix(ci): ...` or the type that fits), push, and run
   `motita forge pr checks --wait --logs` again. Repeat until it exits `0`.
4. If the failure is not caused by this change (the same job is red on the base branch), say so
   with the evidence and ask the user; do not widen the pull request.

Finish by saying the CI result in one line with the link: "[owner/repo#12](url): CI passed".
`motita forge pr status` shows the same report without waiting.

## What not to do

- Do not open a pull request from the base branch, or with uncommitted work left behind.
- Do not force-push or rewrite history on a branch with a pull request unless the user asked.
- Do not merge the pull request: that is the user's decision.
