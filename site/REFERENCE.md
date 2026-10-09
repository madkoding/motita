# motita 🌟

A **3-layer** autonomous agent for i386 machines (and any Linux amd64/arm64),
written in **pure Go**: standard library only, **zero external dependencies**,
no cgo and **no Docker**.

The principle that governs it: **the model proposes, a deterministic validator
disposes**. No task is ever considered complete because the LLM says so; only the
anchor can declare `PASS`, and it does that by running real checks.

```
TASK ──► [Layer B] analyse ─► plan ─► propose an action
                                            │
                            [Layer C] run it isolated
                                            │
                            [Layer A] validate (PASS/FAIL)
                                            │
               PASS ──► final action (commit / publish / notify)
               FAIL ──► failure records back to the LLM, retry
            exhausted ──► escalate
```

Running the binary with no arguments starts an **interactive text user
interface (TUI)**: a conversation with the agent in **Task** mode (it changes files
and proves the change with your check) or read-only **Plan** mode (`Tab` switches),
with `/` opening the command list — the setup (`/config`), the models your provider
publishes (`/models`), the git hosts (`/git`: connect GitHub, GitLab or Bitbucket, or a
self-hosted `gitlab:git.example.com`, by browser login or a token; `/git repos` lists what you can
reach), the sessions the gateway holds, and help (`?`). Task mode and
plan mode are both driven by the same 3-layer agent.

![The command list](screenshots/menu.png)

---

## Architecture

### Layer A — THE ANCHOR (deterministic validator)

A native component that **always** validates the result, with no reasoning:

| | |
|---|---|
| Input | the state of the system after the agent's action |
| Process | strict validations: commands, exit codes, regular expressions over the output, business invariants |
| Output | `PASS`/`FAIL` plus structured JSON records |
| Traits | no LLM, fast and predictable, rules configurable from the YAML |

Design rules the code actually enforces:

- **No validator, no success.** With `anchor.kind=none` the anchor returns `FAIL`
  and the agent **refuses to start**, because there is no authority that can
  declare `PASS`. A `PASS` with no real check is exactly the failure this
  architecture exists to prevent.
- **`anchor.kind=auto` reads the gate the PROJECT declares**, in the directory the
  agent is working in, so one configuration works across projects: `check`/`test`
  from a Makefile, `go test ./...` from a `go.mod`, the `lint`/`typecheck`/`test`
  scripts from a `package.json`, `cargo test`, `pytest`. A project with an unusual
  build names its own gate in `.motita/anchor` (one command per line, `#` comments),
  and that wins over every convention. Detection only READS, so it is idempotent.
  With no gate found the anchor returns `FAIL` and names what it looked for.
- **Every check must pass.** A single failing `check` invalidates the whole
  result.
- **An anchor that cannot be evaluated is a failure, not a silent success**: if
  the command does not exist, times out, or the regular expression does not
  compile, it returns `FAIL` with the reason.
- **The anchor's output is JSON** and it travels whole to the LLM on the next
  attempt, together with the real output of the failed command.

### Layer B — THE REASONING ENGINE (lightweight LLM client)

A hand-written client (no SDK) for three API families: **OpenAI-compatible**
(`/chat/completions`), **Anthropic** (`/v1/messages`) and **Gemini**
(`:generateContent`). The OpenAI-compatible implementation accepts any `base_url`,
so it works with OpenAI, Ollama Cloud, Groq, OpenRouter, DeepSeek, and similar
hosts. All providers are normalised to the same message structure, so the rest of
the agent does not know which one is behind it.

- Reads the context: task, plan, attempt number and **the records of previous
  failures**.
- Dynamic prompts built from templates with `{{...}}` variables, 100%
  configurable: the engine never writes prompt text on its own.
- **Retries with exponential backoff**, with a distinction that matters: `429`,
  `5xx` and network errors are retried; `401`/`400` are **not**, because retrying
  an invalid credential only burns time and quota.
- Structured-response parsing tolerant of what models really return: ```` ```json ````
  blocks, surrounding prose, nested braces, escaped quotes, truncated answers.
- **At most N attempts** before escalating.

### Layer C — THE SANDBOX (light isolation, no Docker)

Layered isolation, applying whatever the system allows and **telling the truth
about what could not be applied**:

| Layer | What it does | Requirements |
|---|---|---|
| Ephemeral temp directory | its own `TMPDIR` per attempt, deleted at the end | none |
| `ulimit` limits | CPU, memory (address space), processes, file descriptors, max file size | none |
| cgroups v1 | a real cap on memory and PIDs (`RLIMIT_AS` is only an approximation) | kernel with cgroups v1 and write permission |
| chroot + privilege drop | restricted filesystem root and an unprivileged user | being root |
| `CLONE_NEWNET` | no network inside the command | `CAP_SYS_ADMIN` |

Details that took real work and are solved in the code:

- **The limits are applied by the shell, not by the agent.** A child process
  which is the binary re-executed (marker `__sandbox_exec`) prepares the ground
  (`chroot`, `chdir`, dropping privileges, resolving the command path) and then
  `syscall.Exec`s `sh -c 'ulimit ...; exec "$@"'`. This is a measured decision,
  not an aesthetic one:
  - a Go binary **cannot** apply `RLIMIT_AS` and stay alive: the limit also
    counts the virtual memory the runtime maps, so the next allocation (sysmon,
    GC, even resolving `PATH`) kills it with `fatal error: runtime: cannot
    allocate memory`. It only showed up on a CI runner with more cores; on a
    small machine it did not appear.
  - `RLIMIT_CPU` does not cut at the exact instant, only at the next scheduling
    of the process. Measured on a single-core machine: with `RLIMIT_CPU` alone
    and no memory limit, an infinite loop with `cpu_seconds: 2` was still alive
    after **12 s** (the limit was never applied because the runtime allocated
    memory in between).
  - with `sh -c 'ulimit …; exec "$@"'` the limited process is exactly the user's,
    the shell disappears with the `exec` (no intermediate process is left behind)
    and neither `prlimit`, nor util-linux, nor privileges are needed.
  - verified: an infinite loop with `cpu_seconds: 2` is cut at **1.994 s**;
    `ulimit -n` inside the command returns the configured value.
- **A `memory_mb` below what the launcher already uses is raised, with a
  warning.** On the target machines (i386 with little RAM) the value is small,
  but it cannot sit below the already-mapped address space: the command would not
  even start. The adjustment is dynamic (real peak ×2), not a constant, and it is
  logged.
- **`syscall.Exec` does not search `PATH`**: a `command: make` written in the
  YAML would fail with `ENOENT`. The child resolves the path using the sandbox's
  **restricted `PATH`**, not the agent's.
- **The working directory is persistent; the temporary one is ephemeral.** The
  effect of the work must survive so the anchor can see it. If the actions ran in
  a directory deleted at the end, the validator would never find the result and
  the agent would fail every time (this really happened during development and is
  covered by a test).
- **Kill the process group, not just the child.** `exec.CommandContext` kills
  `sh`, but its descendants stay alive holding the output pipe open and `Wait`
  keeps waiting: measured, a `sleep 30` with a 1 s deadline took **5 s** to
  return. With its own process group plus `SIGKILL` to the group it cuts at the
  exact deadline (measured: infinite loop with `cpu_seconds: 2` → cut at
  **2.002 s**).
- **The command does not inherit the agent's secrets**: the environment is built
  from scratch, with no `OPENAI_API_KEY` or `MOTITA_LLM_API_KEY` inside the
  command.

---

## Agent flow

```
START
[1] READ TASK     from the configurable source (stdin, file, queue, API)
[2] EXTRACT       context and success criteria (the anchor's rules)
[3] LLM           analyses the task   -> {"understandable", "success_criteria", ...}
[4] LLM           produces a plan     -> {"plan", "subtasks", ...}
[5] SPLIT          into subtasks when the plan declares them (bounded depth)
[6] LLM           produces the action -> {"actions", "final_action", "done"}
[7] RUN            in the SANDBOX (Layer C)
[8] VALIDATE       with the ANCHOR (Layer A) — always, even if [7] failed
    FAIL  -> failure records to the LLM, back to [6] while rejected rounds <= MAX_RETRIES
    PASS and "done": false -> the round's work is recorded, back to [6] for the NEXT batch
    PASS and "done": true  -> [9]
[9] PASS  -> run the final action (command | api | git_commit) and END
    rounds exhausted -> escalate (agent.on_failure) and END
```

Each entry of `actions` has a `kind`. `command` runs a shell line in the sandbox;
`write_file` (path on the first line of `command`, the whole content after it) and
`edit_file` (path, then one `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` block whose
search text must occur exactly once) write files directly, without a shell, so source with
tabs and quotes needs no escaping. Both stay inside the working directory and are refused in
read-only mode. The procedure-library kinds (`list_skills`, `search_skills`, `read_skill`,
`save_skill`) are described in the execute prompt.

When the anchor refuses a claim of done and `anchor.baseline` is on, the failing checks are run
once more on a clean checkout of the tree the run started from. A check that failed there too
is reported as **already failing before this change**: when that is true of every failing
check the claim passes, and the verdict names those checks; a check that passed there is a
breakage the run caused, and the rejection says so. Only the project's own gate
(`anchor.kind: auto`) is compared this way: a check written into the configuration usually
states what the task must achieve, fails before the run by design, and is never excused.

**The loop has TWO bounds, because they answer two different questions.**

- `agent.max_retries` bounds **rejected** rounds: a validation the anchor refused, which
  the model corrects from the failure records it is handed.
- `agent.max_steps` (default 24) bounds **rounds altogether**. A request is usually a
  plan and not a single step, and the model reports whether it is finished through the
  `done` field of its action. With `"done": false` the round is not a failure — it is
  work — so it does not spend the retry budget, and the loop continues with everything
  already run in front of the model.

Without the second bound the anchor decided when to stop, and the anchor validates the
state of the PROJECT: on a healthy repository it passes before any work has happened.
Measured on a real run: a plan of eleven steps, one batch of four actions, and
`task completed` in twelve seconds with the other ten steps untouched.

A run that uses all its rounds with the model still reporting work left fails with a
message that says so — "the task is not finished: N rounds were used and the model still
reports work left" — and never declares PASS.

If the analysis declares the task **not understandable** (information missing),
nothing runs: the task is discarded with the reason and counts as a failure for
the exit code.

---

## Installation

**One line.** It detects the system, downloads the matching static binary from
the latest release, verifies it against the release's `SHA256SUMS`, and puts it
on your `PATH`. No Go, no Docker, no runtime on the target:

```sh
curl -fsSL https://madkoding.github.io/motita/install.sh | sh
```

Override the version or the destination:

```sh
MOTITA_VERSION=v0.3.0 curl -fsSL .../install.sh | sh
MOTITA_INSTALL_DIR="$HOME/bin" curl -fsSL .../install.sh | sh
```

The installer prefers `/usr/local/bin` when it is writable and falls back to
`~/.local/bin`, so it never needs root. It downloads into a temporary directory
and moves the binary into place **only after** the checksum matches: a failed or
corrupted download leaves the system as it was. Finally it runs the binary once,
because reporting success without executing it would miss the one failure that
matters — a binary that does not run on this machine.

`SHA256SUMS` comes from the same release as the binary, so verification catches
a corrupted transfer, not a compromised release.

**On Windows**, in PowerShell:

```powershell
irm https://madkoding.github.io/motita/install.ps1 | iex
```

It does the same four things as the shell installer — detect the architecture,
download the matching `.exe`, verify it against the release's `SHA256SUMS`, and
put it on your `PATH` — using what Windows has: `PROCESSOR_ARCHITEW6432` for the
architecture (a 32-bit PowerShell on 64-bit Windows reports `x86` and must be
corrected, or you silently get the 32-bit binary), `Get-FileHash` for the
digest, and the **user** `PATH` scope, so it never needs administrator rights.

It installs to `%LOCALAPPDATA%\Programs\motita\motita.exe` by default; override
with `$env:MOTITA_INSTALL_DIR`, and pin a release with `$env:MOTITA_VERSION`:

```powershell
$env:MOTITA_VERSION = 'v0.7.0'
irm https://madkoding.github.io/motita/install.ps1 | iex
```

On failure it **throws** instead of exiting, and that is deliberate: the script
is normally run through `irm ... | iex`, and `exit` inside `Invoke-Expression`
ends *your* PowerShell session — a failed download would close the window. A
thrown error is printed, is catchable, and still gives exit status 1 when the
file is run directly.

**By hand**, if you prefer to see each step:

```bash
# Linux / 386 (32-bit x86, the primary target)
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-linux-386 && chmod +x motita-linux-386 && ./motita-linux-386 -version

# Linux / amd64
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-linux-amd64 && chmod +x motita-linux-amd64 && ./motita-linux-amd64 -version

# Linux / arm (ARMv7: Raspberry Pi 2 and newer)
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-linux-arm && chmod +x motita-linux-arm && ./motita-linux-arm -version

# Linux / arm64
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-linux-arm64 && chmod +x motita-linux-arm64 && ./motita-linux-arm64 -version

# macOS / Apple Silicon
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-darwin-arm64 && chmod +x motita-darwin-arm64 && ./motita-darwin-arm64 -version

# macOS / Intel
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-darwin-amd64 && chmod +x motita-darwin-amd64 && ./motita-darwin-amd64 -version

# Windows / amd64 (PowerShell)
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/motita-windows-amd64.exe; if ($?) { ./motita-windows-amd64.exe -version }
```

| System | Architectures | Asset suffix |
|---|---|---|
| Linux | `386`, `amd64`, `arm`, `arm64` | `-linux-<arch>` |
| Windows | `386`, `amd64`, `arm64` | `-windows-<arch>.exe` |
| macOS | `amd64`, `arm64` (Apple silicon) | `-darwin-<arch>` |

`linux/arm` is **ARMv7** (the Go default, `GOARM=7`): it runs on a Raspberry Pi 2
or newer, and on the ARMv8 boards running a 32-bit userland. A Raspberry Pi 1 or
Zero needs an ARMv6 build, which this project does not publish. `windows/arm`,
`darwin/386` and `darwin/arm` are **not** published because Go does not support
those pairs: the toolchain refuses to build them.

Each release also carries a `SHA256SUMS` file:

```bash
curl -fsSLO https://github.com/madkoding/motita/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

## First run: the setup

The binary needs a configuration that names a provider, a model and — most
importantly — the check that decides whether a task is really done. Running `motita`
with no configuration starts the setup on its own; `motita config` runs it on purpose,
and `/config` runs it from inside the interface.

![The setup](screenshots/wizard-onboard.png)

It is five steps, and every step says where it is (`[2/5]`). In a terminal every list
follows `↑` `↓` and Enter takes the highlighted option; typing still works (a number, a
provider's name, any model id), and a pasted key is masked. From a pipe the answers are
read as lines, so a script can answer it:

1. **Provider** — each one listed with what it takes to connect: an API key, a login
   with your account, or nothing (an Ollama server of your own).
2. **Connect** — the endpoint, where it can vary (any OpenAI-compatible host; Ollama on
   this machine, on Ollama Cloud, or at another address), then the sign-in. Providers
   with an account login offer it first; a login that fails is offered again, with
   pasting a key as the alternative. When a key or a login is already saved for this
   provider, **keeping it** is the default.
3. **Model** — the curated list (the first is marked *recommended*), the live catalogue
   for Ollama, or any id typed by hand. A server that does not answer is said in words —
   "nothing is answering there yet (start it with `ollama serve`)" — and the built-in
   list is offered instead.
4. **Check** — detect it from the project (the default: one configuration works on every
   repository), a command you name, or no check for now, recorded as `kind: none` so a
   task is reported as unverified rather than passed.
5. **Review and save** — every answer on one screen, the key masked. `n` goes through
   the questions again; Enter writes the files.

![Review and save](screenshots/wizard-review.png)

What the setup does and does not do:

- Nothing is written before the review is accepted, and `q` at any question leaves.
- The configuration goes to `~/.motita/motita.yaml` (or `./motita.yaml` with no home).
  The one it replaces is kept as `motita.yaml.bak`.
- The key goes to a **separate file** beside it, `motita.env`, with `0600`
  permissions, never into the configuration, so the configuration can be committed or
  shared. **motita reads that file itself** — there is nothing to `source` — and a
  variable exported in the shell still wins over it. A setup with no key removes the
  previous setup's key file, so a key is never sent to a provider it was not given to.
- The setup ends by loading what it wrote, so a broken file is caught immediately.
- Run from the interface (`/config`), the new setup **is applied to the session at
  once** — the provider, its endpoint and key, the model and the check — and the
  conversation says what is in use now. Behind a gateway this is
  `POST /v1/sessions/{id}/config/reload`.

The `ollama` provider talks to `http://localhost:11434/v1` (no key) or to Ollama Cloud
(`https://ollama.com/v1`, a key in `OLLAMA_API_KEY`), and reads the live catalogue from
the server you chose:

```yaml
llm:
  provider: ollama
  base_url: http://localhost:11434/v1
  model:    gpt-oss:20b
```

The provider called `openai` is really **OpenAI-compatible**: it speaks the
OpenAI `/chat/completions` protocol, so you can point it at OpenAI itself, Groq,
OpenRouter, DeepSeek, a self-hosted server, or any other host that implements the
same endpoints. The setup asks for the base URL when you choose it:

```yaml
llm:
  provider: openai
  base_url: https://api.openai.com/v1     # or https://api.groq.com/openai/v1, ...
  model:    gpt-5-mini
```

`/models` inside the interface shows the active provider, the model, whether a key is
present (never the key itself) and the models the endpoint publishes, with the one in
use marked; `/models <id>` switches to one for the session.

![Models and providers](screenshots/models.png)

## Cross-compilation from source

Requires Go 1.26 or newer. **You do not need to compile on the i386 machine.**

The floor is not decoration: the standard library is compiled INTO the published binary, so a
toolchain past its support window ships its known vulnerabilities to whoever downloads the
binary. Go keeps a release for two newer majors and then stops patching it, which is what
happened to the 1.23 this project used to ask for — its standard library carried 26
vulnerabilities reachable from this code (`govulncheck`, go1.23.12), and zero on a supported
series. Two ways to stay ahead of it:

```bash
curl -fsSL "https://go.dev/dl/?mode=json"   # the releases Go still supports
govulncheck ./...                          # what this code can actually reach
```

One wrinkle worth knowing before you trust a red result: `staticcheck` has to run on the
toolchain named in `go.mod`. No released version can read the export data of a much newer Go, so
under a 1.27 toolchain the same code fails with `internal error in importing ... export data
version 4 is greater than maximum supported version 2`, which looks like a finding and is the
tool being unable to parse the compiler's output. It passes on the floor (1.26) and on every
pin tried (v0.6.0, v0.6.1, v0.7.0).

`scripts/verify.sh` checks the first automatically: it reads the floor out of `go.mod`, asks
go.dev which series are still listed, and fails if yours is not among them — so the next
release that retires 1.26 fails the build instead of quietly ageing into an unpatched
binary. With no network it says so and moves on, which is what lets the same script run on
the i386 machine.

```bash
make dist             # one binary for all 9 supported platforms
make test-matrix      # the tests build for every one of them
make check            # gofmt + go vet + go test
ARCH=arm64 make e2e-agent   # end to end in a real container of that architecture
```

Resulting binaries (static, no cgo, no external libraries):

Nine binaries, one per supported platform, all measured under the CI's toolchain and
build flags (`-trimpath -ldflags "-s -w"`, no cgo):

| Binary | Size |
|---|---|
| `dist/motita-linux-386` | 7.21 MB |
| `dist/motita-linux-amd64` | 7.46 MB |
| `dist/motita-linux-arm` | 7.25 MB |
| `dist/motita-linux-arm64` | 6.88 MB |
| `dist/motita-windows-386.exe` | 7.40 MB |
| `dist/motita-windows-amd64.exe` | 7.66 MB |
| `dist/motita-windows-arm64.exe` | 6.96 MB |
| `dist/motita-darwin-amd64` | 7.58 MB |
| `dist/motita-darwin-arm64` | 7.02 MB |

The whole range is 6.88 – 7.66 MB as measured when the interface was a text one; the web
interface, the WebSocket transport and scheduled tasks have since added to that. The
requirement CI enforces is under 20 MB per binary. The sizes move with the Go release, so
treat them as measurements rather than specifications: the gate is the limit, not these
numbers, and it is there to catch runaway growth rather than to bound a feature.

Copy them to the i386 machine over `scp`, `ftp` or USB:

```bash
chmod +x motita-386
./motita-386 -config agent.yaml
```

---

## Configuration

Everything is configurable **without recompiling**. It can be validated without
running anything or calling the LLM:

```bash
motita -config configs/agent.yaml.example -validate-config
motita -config configs/agent.yaml.example -isolation   # what this kernel isolates
```

Every scalar setting can be overridden with a `MOTITA_<BLOCK>_<FIELD>`
environment variable, which **wins over the YAML** (ideal for secrets and
containers). `OPENAI_API_KEY`, `OPENAI_BASE_URL` and `OPENAI_MODEL` are accepted
too. An empty or blank value is ignored, so a stray variable cannot wipe a
setting.

The three settings that are lists or maps (`task_source.headers`, `anchor.args`
and `anchor.checks`) have no variable: they are collections, so they are set in
the YAML file.

The `schedule` block reads `MOTITA_SCHEDULE_ENABLED`, `MOTITA_SCHEDULE_TICK`,
`MOTITA_SCHEDULE_MIN_EVERY` and `MOTITA_SCHEDULE_MAX_RUNS_KEPT`.

One setting also answers to its older name: `agent.graceful_shutdown_timeout` is
`MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT`, and
`MOTITA_AGENT_SHUTDOWN_TIMEOUT` (the name of the earlier release) is still
honoured. The documented one wins when both are set.

| Block | Contents |
|---|---|
| `task_source` | `kind` (`stdin`/`file`/`api`/`queue`), `path`, `dir`, `url`, `method`, `field`, `interval`, `headers`, `body` |
| `anchor` | `kind` (`auto`/`command`/`none`), `command`, `args`, `timeout`, `expect_exit`, `expect_output` (regex), `baseline` (bool, default `true`, `kind: auto` only: a check that fails on a claim of done is run again on a clean checkout of the starting commit, and one that failed there too is reported as already failing instead of blocking), `checks[]` |
| `sandbox` | `kind` (`none`/`chroot`/`cgroups`), `root`, `user`, `memory_mb`, `cpu_seconds`, `processes`, `open_files`, `max_file_size_mb`, `isolate_network`, `cgroups`, `cgroup_root`, `timeout`, `check_timeout`, `keep_ephemeral`, `max_output_kb`, `tools_dir`, `confine_writes` (default `true`: on Linux a command can write only under the working directory, the HOME in the tools directory (not its `bin/` or `tools/`) and its own temp directory, even from a script that changes directory, and cannot read motita's logins or `motita.env`; a command you approve is exempt) |
| `llm` | `provider` (`openai`/`codex`/`copilot`/`ollama`/`anthropic`/`claude-code`/`gemini`/`qwen`), `model`, `api_key`, `base_url`, `max_tokens` (output ceiling; the client learns a model's own limit from the provider's refusal and stays within it, and an answer cut off before any text is asked again with a larger budget; reasoning models spend it on hidden thinking, so give them up to `65536`), `temperature`, `timeout`, `max_attempts`, `backoff_initial`, `backoff_max`, `reasoning{enabled,level}`, `session{context_window,reserve,compact_at,keep_recent}` |
| `prompts` | `analyze`, `plan`, `execute`, `synthesize`, each with `system` and `user` |
| `final_action` | `kind` (`none`/`command`/`api`/`git_commit`), `command`, `args`, `url`, `method`, `commit_message` |
| `agent` | `max_retries`, `max_steps`, `subtask_depth`, `max_parallel` (background agents started with `spawn_agent` running at once, default `3`; `0` turns it off), `max_tasks`, `workspace_dir`, `log_file`, `log_level`, `log_console`, `log_max_mb`, `log_backups`, `graceful_shutdown_timeout`, `read_only`, `shell`, `policy{enforce,strict}`, `on_failure` |
| `skills` | `dir`, `max_file_bytes` |
| `gateway` | `enabled`, `listen`, `token_file`, `allow`, `max_body_kb`, `webui`, `show_actions` (bool, default `false`: the chat shows a counter of the commands and actions a turn ran instead of each one; the web UI's counter expands on click) |
| `schedule` | `enabled`, `tick`, `min_every`, `max_runs_kept` |
| `ui` | `language` (`auto`/`en`/`es`, default `auto`: the language of the terminal, the browser and the setup wizard; `auto` follows the locale and the browser) |

`skills.dir` is the procedure library: the directory of documents the agent may
list, search, read and extend. It defaults to `skills` under the working
directory. The documents **shipped inside the binary** are served even when that
directory does not exist yet, so a fresh install has a library rather than an
empty shelf; your own documents shadow a built-in of the same name.

**A session that belongs to a project gets a library of its own.** Procedures
describe a place — how THIS repository builds, which gate decides here — and a
document about one project offered while you work on another is worse than
nothing: the model reads a confident procedure about a codebase you are not in.
So the library is scoped:

| Session | Sees |
| --- | --- |
| No project | the shared shelf (`skills.dir`) and the shipped procedures |
| Inside a project | its own documents **+** the shared shelf **+** the shipped ones |

A project's own procedures live in `<project>/.motita/skills/`, next to the
checkout rather than in a session's worktree — a worktree is removed when the
session ends, and a procedure written into one would go with it. Inside a project
a document there **shadows** a shared or shipped document of the same name, so a
correction made for one project applies there and nowhere else. A document written
while working in a project is created in that project, so what you learn stays
where you learned it; work done with no project open goes to the shared shelf and
everybody sees it. Nothing written inside a project is visible to another project,
or to a session with no project at all.

`agent.read_only` is plan mode: the agent explores and proposes, and every action
that could change the system is refused before it runs.

### Guardrails, and the two layers of them

`agent.policy` decides how the agent behaves when the model proposes something
consequential, and it has **two layers — only one of which is configurable**.

**The layer you own** is `agent.policy`:

| Setting | Default | Effect |
|---|---|---|
| `enforce` | `true` | Asks before a consequential action runs. Off restores the older behaviour — the model's line runs as written — which is a defensible choice for a batch job with nobody at the keyboard. |
| `strict` | `false` | Off, a line the policy cannot classify is **asked** about. On, it is **refused** instead. |

With `enforce` off the two interact in one direction only: a command that would
have been *asked* about is then *allowed*, and one that `strict` *refuses* stays
refused. Turning the confirmation off can never promote a refusal into a run.

**The layer you do not own** is the floor. It has two groups, and they are not
the same shape.

*Refused by name, whatever the target is:* the `mkfs.*` family,
`fdisk`/`sfdisk`/`cfdisk`/`parted`/`gdisk`/`sgdisk`, `wipefs`, `blkdiscard`,
`shred`, `shutdown`/`reboot`/`halt`/`poweroff`/`init`, and `dd` whenever it names
an output (`of=`). A filesystem is not formatted "safely", and the power state is
not the agent's to decide, so there is nothing to weigh.

*Refused by target:* `rm`, `mv`, `cp`, `ln` and `install` are refused only when
an operand names something the floor protects — `/`, `~` or a home directory, a
system path such as `/etc` or `/usr`, or the tree that **contains** the
workspace. The same verbs against ordinary paths are not the floor's business.

That distinction is worth reading twice, because it is where an operator's
expectation usually differs from the code: the workspace itself is your work and
is *not* guarded — `rm -rf ./build` inside it is the ordinary consequential
action the confirmation layer exists for. What is refused is the directory that
*contains* the work, at any depth above it.

Both groups are measured, not asserted: `internal/policy/readme_claims_test.go`
holds this section to the classifier's real verdicts.

The cost is deliberate and visible. `fdisk -l`, which only *lists* a partition
table, is refused along with the rest of its family, and `dd of=local.img` is
refused although it overwrites nothing anyone needs. The floor is a scan and not
a parser, it prefers refusing to weighing, and every false positive costs one
refusal rather than one filesystem. If you need to inspect a partition table, use
`lsblk` — it is not on the floor.

There is no YAML key, no environment variable and no flag that relaxes any of it.
That is deliberate: a guardrail an operator can switch off is a guardrail that
**will** be switched off — during the incident it was meant for, by whoever wants
the task to finish. If a command is refused by the floor, editing the
configuration will not help you; the refusal names the rule.

The floor is a scan, not a parser, and it looks for a floor program in command
position — first token of a segment, after a wrapper like `sudo`/`env`, after
`xargs`, after `find -exec`, or as the payload of `<shell> -c`. So `sh -c 'rm -rf /'`
is caught, while `echo "rm -rf /"` — a line that merely *mentions* it — is not.
Both directions of error are known: a floor command reached through a program the
scan does not know still arrives at the classifier as that program (refused or
asked about), and a false positive only ever refuses.

### The gateway

The agent has an HTTP face, and it is **on by default**. Running `motita` still
opens the terminal interface, but the same process also listens on loopback behind
a bearer token, so other front ends — a web page, a phone, a desktop window — can
reach **the same conversation** the terminal is having. The agent lives once; every
interface is a client of it.

| Setting | Default | Effect |
|---|---|---|
| `enabled` | `true` | The HTTP face. Off is one deliberate act, for a machine that must not listen at all. |
| `listen` | *empty* | `host:port`, and **empty means "resolve it"**: the wildcard `0.0.0.0:7477`, which is what the program binds unless you name something else. A **fixed** port, because the gateway can outlive the process that started it and a later process has to find it. Port `0` still works and asks the kernel for a free one, but the address then exists only in that process' memory, so nothing else can reach it. The address says where the **socket** is open and nothing about who may connect, which is `allow`. |
| `token_file` | `gateway.token` | Where the bearer token lives, under the motita home. Generated on first use with 32 random bytes, mode `0600`. |
| `allow` | *empty* | **Who may connect**, as an ordered list of rules. Empty means every origin — the fresh-firewall-table default. Entries: `any`, `lan`, an address (`192.168.1.10`), a network (`192.168.0.0/16`), each optionally prefixed with `!` to deny. The first rule that matches decides; an origin no rule matches is allowed. Loopback is always allowed. See the rules table above. Also read from `MOTITA_GATEWAY_ALLOW`, comma- or space-separated. |
| `max_body_kb` | `256` | Cap on a request body. |
| `max_sessions` | `0` | How many conversations one process holds IN MEMORY. `0` means the built-in default (64). The ceiling bounds how many transcripts are resident; conversations beyond it stay on disk and are re-materialised on demand. A negative ceiling is refused rather than read as the default, which would hide the typo that produced it. |
| `artifact_days` | `30` | Days a saved artifact (a file the agent produced, or one you uploaded) is kept; older ones are deleted when the gateway starts and once a day. The folders of sessions and projects that no longer exist are removed too. `0` keeps artifacts for ever; a negative number is refused. Also read from `MOTITA_GATEWAY_ARTIFACT_DAYS`. |

#### Running the gateway as a service

`motita` on its own brings up an interface, and **the agent behind it is a service
that may already be running**. Three outcomes, and which one you get is deliberate:

| Situation | What happens |
|---|---|
| a gateway is already running (found through the service file, or started by `gateway start`) | the interface **attaches to it** and shuts down nothing. A service you started on purpose is not killed because a terminal happened to connect. |
| nothing is running | one is brought up **for this interface**, in-process, and shut down when the interface ends. |
| `-gateway off` | no gateway at all: the interface keeps the direct path it has always had. |

The gateway the interface brings up is **in-process**, which is the opposite of what
`gateway start` does, and the difference matters. The service exists to outlive the shell
that started it. The interface's gateway exists *for* the interface, so it must die with
the process however the process dies — a re-executed child would survive a `SIGKILL` of
the interface and leak, invisibly, until a machine has a dozen agent services and nobody
knows why.

Because the two have to find each other, the gateway says where it is:

```bash
motita gateway start    # starts the service and waits until it answers
motita gateway start -token-only   # prints just the token, for scripts
motita gateway start -json         # url, pid, version, token and links as JSON
motita gateway status   # says whether one is running, where, and WHICH BUILD
motita gateway stop     # stops the one the service file names
```

Those commands name the build they found — `the gateway is running at
http://127.0.0.1:7477 (pid 1234, v0.5.0-70-g9e7baf4)` — because "is one running" is
only half the question. After installing a new binary over an old one, the service
still running can be the **previous build**, and nothing else in the program would
say so. The version is the one the gateway reports about itself through
`/v1/health`, so it names the process that is actually answering rather than the
binary you happen to have on disk.

The library has the same shape of commands, described in
[The skill library](#the-skill-library) below:

```bash
motita curator status   # the thresholds, the last pass, and the lifecycle counts
motita curator run      # one pass now (--consolidate, --dry-run)
motita curator pin build-firmware
motita curator list-archived
```

The interface draws the same version on its top bar, and it always names the build
that is **actually answering**. An interface speaking through a gateway in its own
process names this program; one attached to a gateway that was already running — through
`-connect`, or to a service an earlier `gateway start` left behind — names **that**
gateway, asked over `/v1/health`. The distinction is the whole point: across an upgrade
the service still running is the old build while the binary on disk is the new one, and
drawing the local version there would be a confident lie about the only thing the row
exists to reveal. A version too long for the window is not drawn at all rather than cut
to fit — a truncated version is not one anybody can look up.

`gateway start` **re-executes the program** rather than serving inside the command, so
none of these pay for building a sandbox or a reasoning engine — the service does that,
and `stop`/`status` only read the service file and ask the port a question. The child
receives the same `-config` this command was given: without it, a service started from a
custom configuration would come up on the defaults, and the failure would surface much
later as a client talking to the wrong agent.

The service file lives under the motita home (`gateway.json`, mode `0600`) and is
written **after the bind**, so it carries the effective address rather than the port that
was asked for. An entry naming a gateway that is gone is worse than no entry at all,
because discovery trusts it, so it is removed on the way out.

The default port is **7477**: unassigned in the IANA registry, absent from
`/etc/services`, and below the default ephemeral range on Linux, so it does not compete
with outgoing connections. Some hosts widen that range; if a start fails with `address
already in use`, move it with `-gateway 127.0.0.1:<port>`.


#### Running as a client

`-connect <host:port>` and `-session <id>` make this process a remote control:

| What it needs | What it does not |
|---|---|
| the gateway's address | a sandbox: commands run on the gateway's machine |
| the token, **read** from `gateway.token` (never minted) | a procedure library: it belongs to the gateway |
| network reach to the port | a reasoning engine: the model is called there |

The address may be written with or without a scheme — `127.0.0.1:7477` and
`http://127.0.0.1:7477` are the same thing — because nobody types a scheme for an
address they are looking at.

The token file is read and never created: a client that generated one would present a
credential nobody recognises, get a 401 on every request, and leave a secret in a file
the operator never asked for. If it is missing, the error names the path.

An unknown `-session` is refused at start, and the refusal lists the sessions the gateway
does hold — a dead end becomes a next step, at the cost of one request.

`-p "question"` asks once and prints the answer on **stdout**, with progress on stderr, so
a pipeline gets the answer and nothing else. It is the mode a script uses, and the reason
a client is useful on a machine with no terminal. `-serve` and `-connect` together are
refused: one makes this process the gateway, the other a client of one.

Exposing the gateway is **the posture of a fresh firewall table**: it comes up bound to
the wildcard, and **nothing is restricted until you add a rule**. Rules live in
`gateway.allow` (or `MOTITA_GATEWAY_ALLOW`, comma- or space-separated) and are
**ordered** — the first one that matches decides:

| Rule | Means |
| --- | --- |
| `any` | every origin (also accepted: `all`, `everyone`) |
| `lan` | the private and link-local ranges: `192.168.x`, `10.x`, `172.16-31.x`, `169.254.x`, `fc00::/7`, `fe80::/10` |
| `192.168.1.10` | one address (an `ip:` prefix is accepted too) |
| `192.168.0.0/16` | one network |

Prefix any of them with `!` to **deny** instead of allow:

```yaml
gateway:
  allow: []                   # everything — the default
  allow: ["lan"]              # let the local network in; everything else still gets in
  allow: ["!any"]             # this machine only
  allow: ["lan", "!any"]      # the local network ONLY — the useful one
  allow: ["10.0.0.5"]         # one machine; everything else still gets in
  allow: ["!192.168.1.0/24"]  # everything except one network
```

An origin **no rule matches is allowed**, because the default policy is accept. That is
worth reading twice: `["lan"]` is not "LAN only" — anything outside the LAN still gets in
because nothing denies it. `["lan", "!any"]` is the pair that means LAN only.

**Loopback is always allowed**, whatever the rules say, and that is not a convenience: the
local interface and the local browser reach the gateway over loopback, so a rule set that
locked it out would leave the operator unable to use — or repair — the program they just
configured. `["!any"]` means "this machine only", not "break my terminal".

The rule is applied to **every** request, including `/v1/health` and the page, and it is
applied **before** the token is checked — so a refused origin does not even learn whether
its credential was good. The client's address is read from the **connection**, never from
`X-Forwarded-For` or any other header: honouring one would let anybody reach a gateway
restricted to one office by claiming to be it. (A deployment behind a real reverse proxy
would need a setting naming trusted proxies; that setting does not exist, deliberately.)

#### The browser interface

The gateway serves a web interface **from its own port**. There is no second server and
no second address: `http://127.0.0.1:7477/` is the page, and `/v1/...` on the same
origin is its API. That is why no CORS header exists anywhere — the browser is not
crossing an origin, so there is nothing to negotiate. A test pins the absence, because
the tempting fix for "the web client does not work" is the one change that would hand
the gateway to every page the user has open.

It comes up **with** the gateway, and `gateway.webui: false` (or
`MOTITA_GATEWAY_WEBUI=false`) turns it off for an API-only gateway.

**Getting in.** `motita gateway start` prints one link:

```
the interface is at http://127.0.0.1:7477/#t=<token>
open that link once: the page trades the fragment for a cookie and drops it
```

The token travels in the URL **fragment**, not a query string, and that is the whole
reason for the shape: a browser never sends a fragment to the server, so it does not
appear in a request line, a proxy log or a `Referer`. Open the link once and the page
exchanges it for a cookie, then erases it from the address bar and the history entry.

The cookie is **not the token**. Its value is an HMAC of the token
(`HMAC-SHA256(token, "motita-webui-session-v1")`), so:

| Because it is derived | Consequence |
|---|---|
| what the browser stores is not the token | a cookie lifted from a browser is not a reusable bearer token |
| nothing is stored server-side | no session table, no id to guess, no expiry to sweep |
| it is recomputed per request | **rotating the token invalidates every cookie**, with nothing to clean up |

The cookie is `HttpOnly` (an injected script cannot read it) and `SameSite=Strict`
(another origin never sends it). It is deliberately **not** `Secure`: this gateway
speaks plain http, and a `Secure` cookie over http is one the browser silently refuses
to send — the interface would simply never stay connected, with nothing in any log to
say why.

The page is served **without** a token, like a login form, because it is the only way a
browser can obtain one — and for that same reason it holds no secret at all, which a
test enforces over the bytes that get served. Anyone who can reach the port can see that
a motita gateway is there; on loopback that is the operator, and exposing it to a
network still takes the one deliberate act described above.

`gateway status` deliberately does **not** print the link: it is the command someone
runs in front of another person while asking "is it up?". The token stays readable in
`gateway.token` for anyone who needs the link again.

**What the page does.** It paints the conversation the gateway already holds, streams a
turn as it happens, resumes from the last event id it saw when the connection drops (a
phone changing network does not lose the turn), and shows an approval with the command
**whole** — approving is approving that text, so a truncated command is a different one.

**What it costs.** The page is compiled into the binary. Measured on the dist build:
**+28,672 bytes** on linux/386 and the same order on windows/amd64, against 1.16 MB of
margin under the size gate. Roughly one binary byte per asset byte, so the size of the
page *is* the size of the executable. The one item that can spend the margin is a
webfont — the page uses the system font deliberately, and a test fails if the assets
grow past 64 KB.

**There is no TLS in this version.** Reaching the gateway over the network without a
tunnel sends the token in clear text, so anyone on it can read the credential in
transit — and nothing about the token or the page changes that. The gateway prints a
warning saying so at the moment it starts, because that is when the decision is still
being made. When the two machines can reach each other, the supported way is a tunnel,
which never exposes the gateway at all:

```bash
ssh -N -L 7477:127.0.0.1:7477 the-host
# then point the client at http://127.0.0.1:7477
```

The token is compared in constant time and is never printed by `-validate-config`
or any other command: configuration is shown to clients through a reduced view in
which `llm.api_key` is reported only as *present* or *absent*.

The address is validated at startup, not when the first client arrives: a
`listen` that is not `host:port` is refused. **Who may connect is a separate
question** and is answered by `gateway.allow`, whose entries are parsed while the
configuration is read — a malformed rule is refused with a message naming both the
setting and the offending entry, rather than failing silently at request time.

Whatever is bound, the address a client is **told** is one it can call. A wildcard
bind is reported as `127.0.0.1:<port>` rather than as the `[::]` the kernel reports,
because `[::]` is an address to listen on and not one to dial: in a URL it names no
reachable host, and a browser returns an empty page for it. Whether the gateway is
*reachable from the network* is a separate question with a separate answer, carried
in `gateway.json` as `reachable` and stated in the startup message — alongside
`allow`, the rule set the running gateway is actually enforcing.

**Entering a session means entering it.** `-session <id>` and `/attach <id>` do the same
thing, and both take you back to the conversation rather than to a blank screen:

1. the session is switched, then
2. its turns are read from the gateway and drawn, then
3. **if a turn is in flight there, it is followed** — progress lines arrive as they are
   emitted, the status line shows that work is happening, and Escape stops it by asking
   the gateway.

That third step is not a nicety. A run outlives the client that started it, so arriving at
a session mid-run is the normal case rather than a rare one, and a client that ignored it
would show a static screen next to a conversation that is working — with no way to tell a
live turn from a hung one, and no way to stop the live one.

Following a run and owning one are different things, and the difference is worth knowing
because it decides what your shutdown does:

| | A run this interface started | A run it arrived at |
|---|---|---|
| Escape | stops it | stops it, through the gateway |
| Leaving the interface | the run keeps going | the run keeps going |
| Closing the window | the run keeps going | the run keeps going |

Escape is the one deliberate act, in both columns. Nothing else kills a turn — in
particular, disconnecting from a client does **not**, which is the whole point of a run
living in the gateway. A client that cancelled on the way out would turn a locked phone
screen into a lost turn.

`/sessions` lists the conversations with the most recent first and the one you are in
marked, so the list answers "where was I?".

#### Reconnecting to a turn that is already running

A front end loses its connection for ordinary reasons — a locked phone, a laptop lid, a
tunnel that blinks. The turn does **not** lose its client: a run belongs to its
conversation in the gateway, not to the socket that asked for it. What a client needs is
a way back in, and that is three facts about the wire.

**Every event is numbered.** The stream carries `id: <n>`, and `n` is the sequence number
of the event in the run's log. It is the number a client sends back to say *"I got this
far, continue from here"*:

```
id: 7
event: progress
data: {"text":"a line"}
```

**The first event after attaching is always a preamble.** `event: attached` says what the
client is looking at, and it is what makes *"resumed"* distinguishable from *"I lost
lines"*:

| Field | Meaning |
|---|---|
| `run_id` | Which turn this is. A client that reconnects to a different one knows it. |
| `first_seq` / `last_seq` | The span the log can still serve. |
| `dropped` | How many events were evicted, so a hole is a size and not a suspicion. |
| `pending_approval` | The question being asked **right now**, with its `id`, if the run is blocked on one. |
| `outcome` | Empty while the turn is going; `done`, `error` or `cancelled` once it has ended. A client attaching to a finished turn is **told**, instead of waiting on a stream that will never produce. |
| `agents` | The latest list of the run's agents (see below), when there is one. |

The preamble carries a pending question on purpose. A client that reconnects while the
run is blocked on an approval would otherwise receive a silent stream — and the run would
stay blocked until its client came back and guessed.

**Attaching with nothing running answers 404**, not an empty stream. A stream that will
never produce looks exactly like a hang, and a client that cannot tell the two apart
shows its user a spinner forever.

Resuming is `?from=N`, and the gateway sends what the log still holds after `N`:

```bash
TOKEN="$(cat ~/.motita/gateway.token)"
curl -N -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:7477/v1/sessions/default/events?from=12"
```

The log is bounded, so a client that was away long enough gets a hole instead of the
beginning of the turn. That is deliberate: replaying what the client already has would
make it render the same line twice, and replaying the whole turn to a phone that lost
four seconds is worse than telling it four seconds are gone.

| What | Where |
|---|---|
| `GET /v1/sessions/{id}/run` | Whether a run is in flight, its span, and how many clients are watching it. One JSON reply, so a client can decide before opening a stream. |
| `GET /v1/sessions/{id}/events?from=N` | Attach, from event `N` onwards. |
| `GET /v1/sessions/{id}/agents` | The agents of the run in flight, or of the last run: `{"agents":[...]}`, `[]` when there were none. |
| `POST /v1/sessions/{id}/cancel` | Stop the run in this conversation. |
| `POST /v1/sessions/{id}/runs/approval` | Answer the pending question, by `id`. |

**The run's agents.** A run can start agents of its own in the background. Their list
travels as `event: agents`, `data: {"agents":[...]}`, every time it changes. Like
`thinking` it is ephemeral (`id: 0`, not in the log, not replayed): each one replaces the
one before, and a client that attaches gets the latest in the preamble. Each entry is:

| Field | Meaning |
|---|---|
| `id` / `parent` | The agent, and the one that started it. The main agent has no `parent`. |
| `purpose` | What it was started for, in the words of the agent that started it. |
| `state` | `running`, `passed`, `failed` or `cancelled`. |
| `started` / `finished` | When it started, and when it ended (absent while it runs). |
| `elapsed_ms` | Its running time when the list was sent; count from `started` to show a live clock. |
| `tokens` | `input`, `output`, `cache_read`, `cache_write`, as the provider reported them. |
| `calls`, `round` | Model calls made, and the round it is on. |
| `activity` | Its latest progress line. |
| `branch`, `summary` | Where its work is and what it says it did, once it finished. |

The session list counts them too: `agents_running` is how many background agents (the
main one aside) are working right now. In the terminal, the footer shows the count and
the tokens spent, and **Ctrl+G** or `/agents` opens the full list.

Cancelling is addressed to the **session**, not to a run id, so a client that
reconnected and remembers a stale run cannot stop the wrong turn — or reach a run that
has already ended and report a success that stopped nothing.

A verdict the gateway refuses with `409` (*nothing is waiting for an approval*) is an
error and not something to swallow: it means the client's view is stale.

### Scheduled tasks

A **scheduled task** is a task or a plan the gateway submits on its own, on a cadence,
with nobody at the keyboard. It is a block of its own (`schedule`) because a task
that fires on its own is not a property of the transport: the records live in
`~/.motita/schedules/`, one JSON file per task, and they are meant to be readable and
editable by hand.

```yaml
schedule:
  enabled: true
  tick: 30s         # resolution at which a cadence is NOTICED, not the cadence itself
  min_every: 1m     # shortest cadence a task may be given
  max_runs_kept: 50 # firings kept in a task's record
```

| Endpoint | What it does |
|---|---|
| `GET /v1/schedules` | every task, with its `next_run` computed server-side |
| `POST /v1/schedules` | create one: `title`, `task`, `every` (`"30m"`), optional `kind`, `session_id` |
| `PATCH /v1/schedules/{id}` | change one field: `enabled`, `title`, `task`, `every`, `kind`, `session_id` |
| `DELETE /v1/schedules/{id}` | remove one |
| `POST /v1/schedules/{id}/run` | run it now; `202`, and the cadence is left alone |

```bash
TOKEN="$(cat ~/.motita/gateway.token)"
curl -s -H "Authorization: Bearer ***" http://127.0.0.1:7477/v1/schedules
# {"schedules":[]} — an empty LIST, never null

curl -s -X POST -H "Authorization: Bearer ***" -H 'Content-Type: application/json' \
  -d '{"title":"nightly audit","task":"check the logs and report","kind":"plan","every":"24h"}' \
  http://127.0.0.1:7477/v1/schedules
# 201 with the record, its id and its next_run
```

**There is no cron syntax.** The cadence is a Go duration (`30m`, `6h`, `24h`), and the
reason is that a duration is what the arithmetic needs: `every 6h` means six hours after
the last firing, while `0 */6 * * *` means six hours after midnight, and offering the
second would mean parsing a calendar to answer a question the first already answers. The
minimum is `schedule.min_every` (one minute): a tighter cadence starts a task faster than
an agent turn can finish.

**First firing is one cadence after creation**, and later firings are measured from the
last run. A task created at 10:00 with `every: 6h` fires at 16:00 — not immediately — and
a gateway that restarted twice does not catch up on the firings it missed.

**A task fires INTO a conversation that already exists**, named by `session_id` (the
default one when the field is absent). Creating a conversation per firing would put a run
in a place nobody opened, and the conversations the gateway holds are the only place a run
can be watched: a scheduled turn appears in that session's transcript and is attachable
exactly like one you started by hand.

**A task that is still running is not joined by the next firing.** The conversation's
one-run-at-a-time rule applies, so a firing that arrives while the previous one is in
flight is recorded as skipped with the reason, and the outcome is on the record.

**A scheduled run cannot approve anything.** A command the policy wants to ask about is
refused, and the record says so, because there is nobody to ask — the same behaviour a
batch run already has. The refusal names the fix: set `agent.policy.enforce=false` to run
such work deliberately, which is the documented escape hatch for an unattended job. It is
not a new guardrail and it is not configurable per task: a switch a task could flip is how
a guardrail gets switched off during the incident it was meant for.

### The interface language

The terminal, the browser and the setup wizard speak English or Spanish, chosen by one
setting:

```yaml
ui:
  language: auto   # auto | en | es
```

`auto` (the default) follows the environment each interface runs in: the terminal reads
`LC_ALL`, then `LC_MESSAGES`, then `LANG`, and a value naming Spanish (`es`, `es_CL.UTF-8`)
picks Spanish; the browser uses its own language. `MOTITA_UI_LANGUAGE` overrides the file.
Text a translation does not cover yet is shown in English rather than left blank.

The setting is one for every interface: changing it from the browser's settings or from the
terminal goes through the gateway, which writes it into the configuration file it started
with (in place: comments and every other line are kept).

| Endpoint | What it does |
|---|---|
| `GET /v1/ui` | `{"language":"auto","resolved":"es"}`: the setting, and what it means on the gateway's host |
| `PUT /v1/ui` | `{"language":"es"}` saves it; `400` for anything but `auto`, `en`, `es`, `409` when the gateway runs on the built-in defaults with no file to write |

### The skill library

The library described in the README is served whole: the documents, their
lifecycle, and the maintenance pass that keeps them honest.

```yaml
skills:
  dir: ~/.motita/skills    # where the documents live
  max_file_bytes: 65536    # a bigger document is refused, not truncated

curator:
  enabled: true            # run the maintenance pass on its own
  interval_hours: 168      # how often: a week
  min_idle_minutes: 120    # and only when nobody has been talking for two hours
  stale_after_days: 14     # unused for this long, and it is marked stale
  archive_after_days: 30   # stale for this long, and it moves to the archive
  consolidate: false       # let the model merge overlapping documents
```

| Endpoint | What it does |
|---|---|
| `GET /v1/skills` | the index: name, title, summary, path, and the lifecycle telemetry |
| `GET /v1/skills/{name}` | one document, **with its body** |
| `POST /v1/skills` | write one: `name`, `body` — creates or replaces |
| `POST /v1/skills/{name}/pin` | `{"pinned":true}` exempts it from every automatic transition |
| `POST /v1/skills/{name}/disable` | `{"disabled":true}` stops the agent seeing it: still listed, still readable, one click back |
| `DELETE /v1/skills/{name}` | delete one for good, telemetry included; a shipped procedure is refused with `409` |
| `GET /v1/skills/archived` | the names of everything in the archive |
| `POST /v1/skills/{name}/restore` | bring an archived document back |
| `GET /v1/curator` | the thresholds, the last pass, and the lifecycle counts |
| `POST /v1/curator/run` | run one pass now: `consolidate`, `dry_run` |

```bash
TOKEN="$(cat ~/.motita/gateway.token)"
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7477/v1/skills
# {"skills":[{"name":"build-firmware","title":"Build the firmware",...}]} — never null

curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7477/v1/skills/archived
# {"skills":[]} — an unreadable archive is a 500, NOT an empty list
```

**The index carries no body, deliberately.** Forty procedures fetched to draw forty
titles is how a panel feels slow for no reason, and the body is only what a user
opens a document to read. The lifecycle telemetry rides along instead, so a list can
show state without a round trip per row.

**An unreadable library is not an empty one.** Both list endpoints answer `500` when
the directory cannot be read, rather than an empty list: a browser that drew "no
skills" over a broken directory would send the user looking for documents they still
have.

From the terminal it is the same library:

```bash
motita curator status        # thresholds, last pass, lifecycle counts
motita curator run           # one pass now; --consolidate to use the model, --dry-run to see the plan
motita curator pin build-firmware
motita curator unpin build-firmware
motita curator list-archived
motita curator restore build-firmware
```

**`curator status`, `pin`, `unpin`, `restore` and `list-archived` never need an API
key**, and each one still reads a configuration that names no key — tidying a shelf
is filesystem work, and requiring a model to sort files is requiring a model to do
something that does not use one. `--consolidate` is the exception, deliberately: that
IS the path that talks to a model, so it is the one that needs the key.

**Turning a skill off is not archiving it.** `{"disabled":true}` takes the document out
of the index and out of the search — the two doors the model reaches the library
through — and leaves it exactly where it is: still listed in the interface (badged
`off`), still openable by name, and exempt from every automatic transition, like a
pinned one. It is for a procedure that is right but wrong for now.

**Deleting is the one thing that cannot be taken back**, and it is the only operation
that keeps nothing: the document and its telemetry both go. A shipped procedure is
refused with `409` — it lives inside the binary, and reporting success for a deletion
that did not happen is the one answer worth less than the refusal.

**The curator never deletes.** A document that ages out is *moved* to `.archive/`
inside the skills directory, and `POST /v1/skills/{name}/restore` (or
`motita curator restore`) puts it back. There is no automatic transition that ends in
a missing file, because the pass runs without anybody watching: a maintenance job that
can discard work is a maintenance job nobody leaves switched on. The **user** is the
one who can delete, from the interface that asks for a confirmation first, and that is
the only route to a missing file in this library.

**Only agent-written documents are aged.** The pass reads `created_by` off the ledger
and skips everything else: a document you wrote by hand is yours, and the machine does
not get to call it stale. Setting `pinned` takes a document out of the pass entirely.

The status codes are the ordinary ones, and they are worth naming because a client
has to tell them apart: `201` when `POST /v1/skills` creates, `204` from `pin`,
`disable` and `restore`, `404` for a name that is not there, `409` when a deletion is
refused because the document ships inside the binary, and `501` from every endpoint
when the process was started without a library at all — which is a different answer
from "the library is empty", and the reason a front end can say which one it is
looking at.

### What runs silently, and why that is a design decision

The confirmation layer is the part operators actually feel, because it is the part
that interrupts them. Three rules decide whether a line runs without a question,
and only the third exists to keep the layer usable:

1. **Reads run.** `ls`, `cat`, `grep`, `find` without a destructive action — they
   change nothing, so there is nothing to weigh.
2. **The project's own build runs.** The local toolchains — `make`, `go build`,
   `go test`, `npm test`, `cargo build`, `python3 script.py` — and your own
   `./scripts/*`, when the script lives **inside the workspace**, are recognised
   as the project's own work and run silently.
3. **Everything else is a question.** A program that reaches the network or the
   system, a write outside the workspace, and anything the policy cannot place.

Rule 2 is the one that keeps the layer alive. A policy that asked about `make`
would be switched off within a week, and then it would protect nothing — so the
line between "the project's work" and "something nobody can predict" has to be
drawn somewhere, and it is drawn at whether the policy **can see** that the work
is local.

The boundary cases are deliberate, and each one is a test:

| Line | Verdict | Why |
|---|---|---|
| `python3 build.py` (inside the workspace) | silent | a script that belongs to the project |
| `./scripts/deploy.sh` | silent | the same, when you name it directly |
| `sh scripts/deploy.sh` | **asked** | a shell interpreter is opaque by construction — see below |
| `cd /elsewhere`, `pushd`, `git -C /elsewhere`, `make -C /elsewhere` | asked | the task works in its own directory; a line that walks out of it runs the rest against another tree |
| a program that is not installed (not on the sandbox `PATH`) | asked | the question says it is missing and offers the way forward: approve the install, or install it yourself and retry |
| `python3 /opt/other/build.py` | asked | a script from outside — the policy cannot read it as yours |
| `python3 -c '...'` | asked | inline code is as opaque as a shell line |
| `make`, `go build`, `npm test` | silent | the local toolchain |
| ``sed -n '1,300p' file``, ``awk '{print $1}' file`` | silent | the line editors in their printing form, which is how a file gets read |
| `sed -i 's/a/b/' file`, ``awk '{print > "f"}'`` | **asked** | the same programs in their writing form: a flag, or a redirection in the script |
| `sort -o out.txt in.txt`, `uniq in.txt out.txt`, `xxd in.bin out.hex` | **asked** | a reader whose second operand is an output file |
| `pip install x`, `git push` | asked | it reaches the network |
| `htop`, an unknown binary | asked | nobody can classify it; with `strict` on, refused |

**A program's name is not the answer about the program.** The rows above with two entries
are the same tool asked twice, and the answer differs because the ARGUMENTS differ. `sed`
was once in the writers table, so `sed -n '1,300p' file` — the way a coding agent looks at a
file, measured 197 times in one real session — was refused in plan mode and put in front of
the user as a question in every other mode, while the program was being used as a reader.
The mirror of that defect is worse: `sort -o out.txt in.txt` and `uniq in.txt out.txt` were
listed as readers with no rule, so they ran **in silence while writing a file**.

Both are fixed the same way: a program that reads in one argument list and writes in another
gets a rule, and the rule is consulted **before** the reader list. The two shapes of rule lean
in different directions, deliberately. A rule that **counts operands** (`uniq`, `xxd`) cannot
know whether an unrecognised flag takes a value, so a separate value counts as an operand —
`xxd -l 16 f` would be refused as a write without the per-program table of options it does
take. A rule that **scans flags** (`sed`, `awk`, `yq`, `xmllint`, `base64`, `sort`) refuses the
writing flags it knows and treats the rest as reads, because refusing every unrecognised flag
would interrupt the ordinary work of looking at a file. Two forms write and are still not
refused, because no lexical rule can tell them from the reading use: `awk '{print > out}'` (an
unquoted redirection target, which reads exactly like the comparison `$1 > out`) and
`sed -n '1w out' f` (a `w` command inside the script, which needs no `-i`). They are recorded
as known gaps in `internal/readonly/argument_rules_test.go` rather than papered over with a
rule that guesses.

There is one asymmetry in that table worth naming, because it is the row an
operator is most likely to trip over: **`./scripts/deploy.sh` runs silently but
`sh scripts/deploy.sh` is asked about.** An interpreter is opaque by
construction — `sh -c 'ls'` and `sh -c 'rm -rf /'` have the same shape, and the
policy cannot read the script it is handed as data. Naming the program directly
is different: the policy can see that the path resolves inside the workspace, so
the project's own tooling is recognised as the project's own tooling. Handing the
same file to a shell hides that from the classifier, and the cautious answer is
to ask. This is deliberate, and it is the same judgement that makes inline code a
question.

Inline flags are read per interpreter, because `-c` does not mean the same thing
to `python3` and to `gcc`: treating them alike would either silence a real
question or ask about a compile flag. An assignment in front of a command
(`FOO=1 ls`) is classified by the **real** program, not by the assignment.

### Variables available in prompts

`{{task}}` `{{workspace}}` `{{origin}}` `{{attempt}}` `{{max_attempts}}`
`{{rules}}` `{{analysis}}` `{{plan}}` `{{history}}` `{{model}}`
`{{provider}}` `{{context_*}}`

If a template uses a variable with no value, **a warning is logged** and the
variable is left visible: the model is never handed a prompt with silent holes.

The YAML parser is hand-written (no dependencies) and **refuses with an explicit
message** whatever it does not understand: unknown keys (naming the block they
are in), tabs in the indentation, anchors/aliases/tags and malformed lists. It
never guesses.

### The four use cases

| Case | File | Flow |
|---|---|---|
| 1. Development | `configs/cases/1-development.yaml` | task in a file → LLM → networkless sandbox → **`go test` + `go vet` + `gofmt` as the anchor** → commit |
| 2. Data analysis | `configs/cases/2-data.yaml` | task from an API → isolated networkless analysis → **report invariants as the anchor** → publish the result over the API |
| 3. Automation | `configs/cases/3-automation.yaml` | file queue → bounded script (256 MB, 30 s CPU, no network) → **effect check as the anchor** → notification |
| 4. Audit | `configs/cases/4-audit.yaml` | audit request in a file → read-only code and security review in a networkless sandbox → **report validated by `scripts/verify-audit-report.sh` as the anchor** (structure, severity, remediation, every cited `path:line` exists, no secret in clear, no tracked file modified) → published report |

All four are verified by the test suite: if an example YAML stops loading, or
loses one of its three templates, the tests fail.

---

## Usage

```bash
export MOTITA_LLM_API_KEY=sk-...        # never the key in the YAML

# normal run, with the configured source
motita -config configs/cases/1-development.yaml

# a single task, without touching the configuration
motita -config configs/cases/1-development.yaml -task "fix TestFoo"

# the contents of a file as the task
motita -config configs/cases/2-data.yaml -task-file task.md

# validate the configuration without calling the LLM
motita -config my.yaml -validate-config

# see the isolation actually available on this machine
motita -config my.yaml -isolation

# read-only plan mode: ask a single question and get a plain-text plan
motita -p "list the .go files and suggest a refactor"
```

![Plan mode](screenshots/plan.png)

Plan mode is **structurally read-only**: the agent calls tools (`read_file`,
`execute_command`) through a path that never invokes a shell, so redirections,
pipes and shell metacharacters are syntactically impossible. Destructive
commands are refused before they run.

**Graceful shutdown:** `Ctrl+C` (or `SIGINT`/`SIGTERM`) works everywhere. The first
signal cancels the current work in progress, returns from the TUI or `motita config`
wizard, and grants `agent.graceful_shutdown_timeout` seconds to finish; the
second signal exits immediately with code 130.

**Exit code:** `0` if every task passed the anchor, `1` if any failed (including
each one's reason *in the error message itself*) and `2` if the configuration is
invalid. That makes it directly usable from cron or systemd.

---

## Logging

JSON Lines, one line per event, with size-based rotation:

```json
{"ts":"2026-09-17T05:17:37.726157258Z","level":"warn","msg":"anchor validation","pass":false,"reason":"failed checks: main"}
{"ts":"2026-09-17T05:17:37.733481067Z","level":"info","msg":"anchor validation","pass":true,"reason":"1 check(s) passed"}
{"ts":"2026-09-17T05:17:37.736394109Z","level":"info","msg":"validation passed","attempt":2,"final_action":"command exit=0"}
```

```bash
jq 'select(.level=="error")' workspace/motita.log
jq -r 'select(.msg=="task completed") | .task' workspace/motita.log
```

`log_max_mb` and `log_backups` control the rotation (`motita.log.1`, `.2`, …).

---

## Verification

```bash
make check           # gofmt + go vet + go test  (what CI runs)
make cover           # coverage per package and aggregate
make e2e-agent       # agent end to end in a real i386 container
make e2e             # one-shot task end to end in a real i386 container
./scripts/verify.sh  # everything above, in order, with a coverage gate
```

- **Hundreds of test cases**, all green, over the three layers and their parts:
  anchor, sandbox, LLM engine, agent loop, YAML parser, logging, templates and
  task sources.
- **100% of statements covered in every package** (`make cover` prints one line
  per package, and CI fails if any of them drops below 100%). It is not
  decoration: the tests are what found the real bugs listed below.
- The end-to-end tests run the binaries inside a real 32-bit container, so what is
  verified is the artifact that is published, not a host build.
- The CI (`.github/workflows/ci.yml`) runs `gofmt`, `vet`, `test -race`, enforces
  a coverage gate, builds the binary for `linux/386`/`amd64`/`arm`/`arm64`, **verifies
  the i386 binary is ELFCLASS32** (reading the ELF header and also with `file`),
  runs it inside `i386/debian:bookworm-slim`, runs both end-to-end tests in that
  container and publishes the binaries when a `v*` tag is created.

### End-to-end test

The e2e targets run in a container with **docker or podman**: docker when its daemon
answers, otherwise podman (no daemon needed). `CONTAINER_RUNTIME=podman` (or `docker`)
forces one; see `scripts/container-runtime.sh`.

`make e2e-agent` builds the agent and a simulated LLM (`tools/mockllm`) for 386
and runs them **inside the same 32-bit container**. The simulated model gets it
wrong on purpose on the first attempt and corrects itself on the second, so the
whole real cycle is exercised:

```
phase=analyze -> phase=plan -> phase=execute (attempt 1, writes invalid contents)
{"msg":"anchor validation","pass":false,"reason":"failed checks: main"}
{"msg":"attempt failed","attempt":1,"max_attempts":3}
phase=execute (attempt 2, writes content-valid)
{"msg":"anchor validation","pass":true,"reason":"1 check(s) passed"}
{"msg":"validation passed","attempt":2,"final_action":"command exit=0"}

E2E of the 3-layer agent on i386: OK
  ok  the report holds the requested contents: content-valid
  ok  the final action ran after PASS
  ok  there was a failed attempt before PASS (the retry worked)
```

The test checks the result **on the filesystem**, not in what the agent says
about itself.

---

## Troubleshooting (i386)

| Symptom | Cause and fix |
|---|---|
| `cgroups v1 do not appear to be available` (warning, not an error) | The kernel or the container does not expose `/sys/fs/cgroup/memory`. The ulimit limits still apply and the warning is logged so isolation is never faked. |
| `chroot requested but the process is not root` | The chroot is skipped with a warning. Run as root or use `kind: cgroups`. |
| `the command could not be run inside the sandbox (code 127)` | The command does not exist or is not on the sandbox `PATH`; the child's error names the path it looked for. |
| `the process was terminated by a signal` | A sandbox limit did its job (CPU or memory). Raise `cpu_seconds` / `memory_mb`. |
| The agent always fails with `ran out of attempts` | Look at the anchor's `reason`: it is in the error message and in the log. It is usually a badly configured anchor, not the model. |
| `anchor.kind=none: this agent only declares a task complete...` | Intentional: without a deterministic validator there is no `PASS`. Configure a real anchor. |
| `anchor.kind=auto found no gate to run in ...` | The directory the agent works in declares no gate. The reason names every convention that was looked for: add `.motita/anchor` with the project's command, or set `anchor.kind=command`. |
| The binary will not start (`not found`) | It is a 32-bit ELF: `head -c 5 binary \| od -An -tx1` must start with `7f 45 4c 46 01`. |
| `output truncated by the sandbox limit` | Raise `sandbox.max_output_kb` if the command produces more output than expected. |
| `fatal error: runtime: cannot allocate memory` | A `memory_mb` that is too low. The agent detects it (it compares against the address space the launcher needs), raises it and logs a warning. If it still appears, raise `memory_mb`. |
| `memory_mb: applying N MB instead of M` (warning) | The configured value was below the address space the launching process already uses, so the command would not have started. The minimum viable value is applied; the warning is in the log and in `-isolation`. |
| A task shows as not completed even though the anchor gave PASS | The **final action** failed (commit, publication, notification). It counts as a failure on purpose: a contract that is not honoured is not a success. The reason is in the log and in the error message. |

---

## Project layout

```
cmd/agent/            the 3-layer agent and TUI (main program)
internal/anchor/      Layer A: deterministic validator
internal/llm/         Layer B: OpenAI / Anthropic / Gemini, text and tool calling
internal/sandbox/     Layer C: ephemeral dir, ulimit limits, cgroups, chroot
internal/config/      YAML (own parser), environment and validation
internal/execx/       process execution (process group, limits, output)
internal/task/        task sources: stdin, file, queue, API
internal/template/    the {{...}} variables of the prompts
internal/app/         program logic (options, layers, shutdown, TUI wiring)
internal/tui/         interactive text user interface (stdlib only)
internal/plan/        read-only plan/chat mode with native tool calling
internal/skills/        the procedure library: the documents, and their archive
internal/curator/       the maintenance pass: stale, archive, and consolidation
internal/procedures/    the library as the tools see it (search, load, save)
internal/usage/         the ledger next to the shelf (/good, /bad, lifecycle telemetry)
internal/gateway/       HTTP + WebSocket gateway (sessions, projects, schedules, skills, git logins)
internal/gitforge/      git hosts: logins, credential helper, repositories, pull requests, CI
internal/oauth/         direct logins (device code, PKCE, refresh) for the providers and the git hosts
internal/semantic/      Conventional Commits check for commit subjects and pull request titles
internal/webui/         the browser interface's built assets
internal/logx/        JSON logging with rotation
internal/readonly/    structural read-only guarantee (no shell + allowlist)
tools/mockapi/        OpenAI-compatible API for end-to-end tests
tools/mockllm/        simulated LLM for the agent's E2E
configs/              example configuration + 3 use cases
scripts/              end-to-end tests
```

Every package keeps its tests next to the code (`*_test.go`).

---

## Extending the agent

- **Another task source**: implement the `task.Source` interface (`Next`, `Close`,
  `Describe`) and add it to `task.New`.
- **Another LLM provider**: add its dialect in `internal/llm` (a
  `call<Provider>` function); the rest of the agent does not change.
- **Another final action**: add a case in `(*Agent).runFinalAction`.
- **Observe results**: assign `Agent.Observer` to receive the `TaskResult` of
  every task (metrics, integration, tests).
- **Test without external programs**: assign `Agent.ExecuteCommand` to inject the
  command runner (that is how the `git_commit` path is tested without git).

## License

GPL-3.0-or-later — see [LICENSE](LICENSE). The copyright notice to carry in a modified
version is quoted in the README, because section 5 of the GPL does not allow the licence
document itself to be changed.
