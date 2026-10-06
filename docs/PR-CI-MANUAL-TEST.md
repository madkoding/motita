# Trying "Create PR" and the CI loop against a real repository

The gateway's tests drive this flow against a fake host. This is the one run that needs a real
one: a throwaway repository, a CI that fails on purpose, and about ten minutes. Do it after
changing `internal/gateway/pr*.go`, `internal/gitforge/` or the PR bar in `web/src/App.tsx`.

## Set up (once)

1. Make an empty repository on GitHub (or any host motita connects to), for example `motita-ci-sandbox`.
2. Add a workflow that runs one real check, `.github/workflows/ci.yml`:

   ```yaml
   name: ci
   on: [pull_request]
   jobs:
     test:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - run: sh ./check.sh
   ```

3. Add `check.sh` that **fails until the fix exists**, and a `README.md`:

   ```sh
   #!/bin/sh
   # Passes only when FIXED exists. The agent is asked to find out why it fails and make it pass.
   test -f FIXED || { echo "FIXED is missing: create it with the text 'ok'"; exit 1; }
   grep -qx ok FIXED
   ```

   Commit both to `main`.
4. In motita: **Settings → Git connections** → connect the host. Then **New project** from that
   repository. In the project's edit dialog set *Attempts to fix a failing CI* to `3` and leave
   *Merge automatically* off.

## The run

| Step | Do | Expect |
| --- | --- | --- |
| 1 | In a new session ask: "Add a line to the README about the sandbox." | work appears; the bar shows **Create PR** |
| 2 | Press **Create PR** (allow notifications when asked) | the agent commits, pushes, opens the PR with a semantic title; the bar shows `Pull request #N` and *CI running* |
| 3 | Wait | the CI fails (no `FIXED`); a **CI failed** toast says *attempt 1 of 3*; the chat shows the agent reading the log |
| 4 | Wait | the agent creates `FIXED`, commits (`fix(ci): …`), pushes; the bar goes back to *CI running* |
| 5 | Wait | the CI passes: **CI passed** toast, a browser notification if the tab is in the background, and **Merge** appears |
| 6 | Press **Merge**, then press it again | the PR merges on the host; the toast says merged; the bar offers **Continue in a new session**; the PR branch is gone |
| 7 | Check the project directory | it is fast-forwarded to the merged `main` (clean checkouts only) |

## Variants worth one run each

- **Gives up**: set attempts to `1` and make `check.sh` impossible to satisfy → *Motita could not fix the CI*, a **Try again** button; pressing it starts over with the count at zero.
- **Base is red too**: break `check.sh` on `main` first → the watch stops with *The CI is red on the base branch too* and no attempt is spent.
- **Merged elsewhere**: after the CI passes, merge from the host's page → within about a minute the session turns *merged* and the project is brought up to date.
- **Closed**: close the PR on the host instead → *Pull request closed*.
- **Needs approval**: turn on a branch protection rule requiring one review → the bar says *Waiting for approvals or required checks* and **Merge** is hidden.
- **Auto-merge**: turn it on in the project → the PR merges itself the moment the CI passes (and not while a review is missing).
- **Restart**: restart the gateway while the CI runs → the watch resumes and the toasts still come.
- **Terminal**: attach a terminal to the gateway during the run → a notice (and the bell) in the footer at each step; `/sessions` shows `[PR #N: …]`.

## Clean up

Delete the sandbox repository, and the project in motita.
