# starlight 🌟

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
interface (TUI)** that lets you choose between read-only plan mode, task mode,
the configuration wizard, a screen that shows the provider and the models it
publishes, and help. Task mode and plan mode are both driven by the same 3-layer
agent.

![Main menu](docs/screenshots/menu.png)

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
  from scratch, with no `OPENAI_API_KEY` or `STARLIGHT_LLM_API_KEY` inside the
  command.

---

## Agent flow

```
START
[1] READ TASK     from the configurable source (stdin, file, queue, API)
[2] EXTRACT       context and success criteria (the anchor's rules)
[3] LLM           analyses the task   -> {"understandable", "success_criteria", ...}
[4] LLM           produces a plan     -> {"plan", "subtasks", ...}
[5] SPLIT          into subtasks if the analysis asks for it (bounded depth)
[6] LLM           produces the action -> {"actions", "final_action"}
[7] RUN            in the SANDBOX (Layer C)
[8] VALIDATE       with the ANCHOR (Layer A) — always, even if [7] failed
[9] PASS  -> run the final action (command | api | git_commit) and END
    FAIL  -> failure records to the LLM, back to [6] while attempts < MAX
    exhausted -> escalate (agent.on_failure) and END
```

If the analysis declares the task **not understandable** (information missing),
nothing runs: the task is discarded with the reason and counts as a failure for
the exit code.

---

## Installation

**One line.** It detects the system, downloads the matching static binary from
the latest release, verifies it against the release's `SHA256SUMS`, and puts it
on your `PATH`. No Go, no Docker, no runtime on the target:

```sh
curl -fsSL https://raw.githubusercontent.com/madkoding/starlight/main/scripts/install.sh | sh
```

Override the version or the destination:

```sh
STARLIGHT_VERSION=v0.3.0 curl -fsSL .../install.sh | sh
STARLIGHT_INSTALL_DIR="$HOME/bin" curl -fsSL .../install.sh | sh
```

The installer prefers `/usr/local/bin` when it is writable and falls back to
`~/.local/bin`, so it never needs root. It downloads into a temporary directory
and moves the binary into place **only after** the checksum matches: a failed or
corrupted download leaves the system as it was. Finally it runs the binary once,
because reporting success without executing it would miss the one failure that
matters — a binary that does not run on this machine.

`SHA256SUMS` comes from the same release as the binary, so verification catches
a corrupted transfer, not a compromised release.

**By hand**, if you prefer to see each step:

```bash
# Linux / 386 (32-bit x86, the primary target)
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-linux-386 && chmod +x starlight-linux-386 && ./starlight-linux-386 -version

# Linux / amd64
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-linux-amd64 && chmod +x starlight-linux-amd64 && ./starlight-linux-amd64 -version

# Linux / arm (ARMv7: Raspberry Pi 2 and newer)
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-linux-arm && chmod +x starlight-linux-arm && ./starlight-linux-arm -version

# Linux / arm64
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-linux-arm64 && chmod +x starlight-linux-arm64 && ./starlight-linux-arm64 -version

# macOS / Apple Silicon
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-darwin-arm64 && chmod +x starlight-darwin-arm64 && ./starlight-darwin-arm64 -version

# macOS / Intel
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-darwin-amd64 && chmod +x starlight-darwin-amd64 && ./starlight-darwin-amd64 -version

# Windows / amd64 (PowerShell)
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/starlight-windows-amd64.exe; if ($?) { ./starlight-windows-amd64.exe -version }
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
curl -fsSLO https://github.com/madkoding/starlight/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

## First run: the wizard

The binary needs a configuration that names a provider, a model and — most
importantly — the check that decides whether a task is really done. `-init` asks
for the three and writes a file that works:

```bash
./starlight -init
```

```
Welcome to starlight.
This wizard writes a working configuration in ./starlight.yaml.
Nothing is written until every answer is in: press q to cancel at any point.
Which provider will run the reasoning?
  1. OpenAI-compatible (openai)
  2. Ollama Cloud (ollama)
  3. Anthropic (anthropic)
  4. Google Gemini (gemini)

Provider [1]: 2

The key is read from OLLAMA_API_KEY, or from STARLIGHT_LLM_API_KEY.
You can get one at https://ollama.com/settings/keys

Paste the key, or press Enter to set it later: 

Which model from Ollama Cloud?
  1. nemotron-3-ultra
  2. gpt-oss:20b
  ...

Model [1, or type any model id]: 7

What decides that a task is really done?
  1. A command that must succeed (for example: make test)
  2. Always pass, while I try the agent out

Check [1]: 1
Command to run as the check [make]: make test

✅ Written ./starlight.yaml
   provider: Ollama Cloud (ollama)
   model:    deepseek-v4.1-flash
✅ Written ./starlight.env
   permissions 0600, keep it out of the repository
```

### Ollama Cloud

Ollama Cloud is a provider of its own in the wizard. It always talks to
`https://ollama.com/v1`, asks **only for the key**, and then reads the live
catalogue from the API so you pick a model from what your account can actually
run — no URL to remember and no model list to keep up to date by hand:

```yaml
llm:
  provider: ollama
  base_url: https://ollama.com/v1
  model:    deepseek-v4.1-flash
```

The key is read from `OLLAMA_API_KEY` (the name Ollama itself documents) or from
`STARLIGHT_LLM_API_KEY`, in that order of preference. If the catalogue cannot be
reached, the wizard falls back to a built-in list and says so, so you are never
left with an empty menu.

The interactive menu has a **Models & providers** entry that shows the active
provider, the model, whether a key is present (never the key itself) and the
models the endpoint publishes, with the one in use marked:

![Models and providers](docs/screenshots/models.png)

The provider called `openai` is really **OpenAI-compatible**: it speaks the
OpenAI `/chat/completions` protocol, so you can point it at OpenAI itself, Groq,
OpenRouter, DeepSeek, a self-hosted Ollama, or any other host that implements the
same endpoints. The wizard asks you for the base URL when you choose it:

```yaml
llm:
  provider: openai
  base_url: https://api.openai.com/v1     # or https://api.groq.com/openai/v1, ...
  model:    gpt-4o-mini
```

Then:

```bash
source ./starlight.env                                       # the key, if you pasted one
./starlight -config ./starlight.yaml -validate-config  # does it load?
./starlight -config ./starlight.yaml -task "what to do"
```

What the wizard does and does not do:

- The three providers are the ones the client implements (OpenAI-compatible,
  Anthropic, Gemini). The model list is a shortcut: **any** model id can be typed
  by hand. The `openai` provider accepts any `base_url` that speaks the OpenAI
  `/chat/completions` protocol.
- The check (layer A, the anchor) is asked because the agent refuses to run
  without one: it never takes the model's word that a task is done. Option 2
  writes `command: "true"`, an explicit "everything passes" while you try it out.
- The key goes to a **separate file** with `0600` permissions, never into the
  configuration, so the configuration can be committed or shared.
- Nothing is written if you cancel: the file appears only once every answer is in.
- With no `-config` the destination is `./starlight.yaml`.
- The wizard ends by loading what it wrote, so a broken file is caught immediately.

## Cross-compilation from source

Requires Go 1.23 or newer. **You do not need to compile on the i386 machine.**

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
| `dist/starlight-linux-386` | 6.41 MB |
| `dist/starlight-linux-amd64` | 6.69 MB |
| `dist/starlight-linux-arm` | 6.50 MB |
| `dist/starlight-linux-arm64` | 6.44 MB |
| `dist/starlight-windows-386.exe` | 6.58 MB |
| `dist/starlight-windows-amd64.exe` | 6.88 MB |
| `dist/starlight-windows-arm64.exe` | 6.42 MB |
| `dist/starlight-darwin-amd64` | 6.85 MB |
| `dist/starlight-darwin-arm64` | 6.55 MB |

The whole range is 6.41 – 6.88 MB, and the requirement CI enforces is under 10 MB per
binary. The sizes move with the Go release, so treat them as measurements rather than
specifications: the gate is the limit, not these numbers.

Copy them to the i386 machine over `scp`, `ftp` or USB:

```bash
chmod +x starlight-386
./starlight-386 -config agent.yaml
```

---

## Configuration

Everything is configurable **without recompiling**. It can be validated without
running anything or calling the LLM:

```bash
starlight -config configs/agent.yaml.example -validate-config
starlight -config configs/agent.yaml.example -isolation   # what this kernel isolates
```

Every scalar setting can be overridden with a `STARLIGHT_<BLOCK>_<FIELD>`
environment variable, which **wins over the YAML** (ideal for secrets and
containers). `OPENAI_API_KEY`, `OPENAI_BASE_URL` and `OPENAI_MODEL` are accepted
too. An empty or blank value is ignored, so a stray variable cannot wipe a
setting.

The three settings that are lists or maps (`task_source.headers`, `anchor.args`
and `anchor.checks`) have no variable: they are collections, so they are set in
the YAML file.

One setting also answers to its older name: `agent.graceful_shutdown_timeout` is
`STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT`, and
`STARLIGHT_AGENT_SHUTDOWN_TIMEOUT` (the name of the earlier release) is still
honoured. The documented one wins when both are set.

| Block | Contents |
|---|---|
| `task_source` | `kind` (`stdin`/`file`/`api`/`queue`), `path`, `dir`, `url`, `method`, `field`, `interval`, `headers`, `body` |
| `anchor` | `kind` (`command`/`none`), `command`, `args`, `timeout`, `expect_exit`, `expect_output` (regex), `checks[]` |
| `sandbox` | `kind` (`none`/`chroot`/`cgroups`), `root`, `user`, `memory_mb`, `cpu_seconds`, `processes`, `open_files`, `max_file_size_mb`, `isolate_network`, `cgroups`, `cgroup_root`, `timeout`, `keep_ephemeral`, `max_output_kb` |
| `llm` | `provider` (`openai`/`anthropic`/`gemini`), `model`, `api_key`, `base_url`, `max_tokens`, `temperature`, `timeout`, `max_attempts`, `backoff_initial`, `backoff_max`, `reasoning{enabled,level}`, `session{context_window,reserve,compact_at,keep_recent}` |
| `prompts` | `analyze`, `plan`, `execute`, `synthesize`, each with `system` and `user` |
| `final_action` | `kind` (`none`/`command`/`api`/`git_commit`), `command`, `args`, `url`, `method`, `commit_message` |
| `agent` | `max_retries`, `subtask_depth`, `max_tasks`, `workspace_dir`, `log_file`, `log_level`, `log_console`, `log_max_mb`, `log_backups`, `graceful_shutdown_timeout`, `read_only`, `shell`, `policy{enforce,strict}`, `on_failure` |
| `skills` | `dir`, `max_file_bytes` |

`skills.dir` is the procedure library: the directory of documents the agent may
list, search, read and extend. It defaults to `skills` under the working
directory. The documents **shipped inside the binary** are served even when that
directory does not exist yet, so a fresh install has a library rather than an
empty shelf; your own documents shadow a built-in of the same name.

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
| `python3 /opt/other/build.py` | asked | a script from outside — the policy cannot read it as yours |
| `python3 -c '...'` | asked | inline code is as opaque as a shell line |
| `make`, `go build`, `npm test` | silent | the local toolchain |
| `pip install x`, `git push` | asked | it reaches the network |
| `htop`, an unknown binary | asked | nobody can classify it; with `strict` on, refused |

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

### The three use cases

| Case | File | Flow |
|---|---|---|
| 1. Development | `configs/cases/1-development.yaml` | task in a file → LLM → networkless sandbox → **`go test` + `go vet` + `gofmt` as the anchor** → commit |
| 2. Data analysis | `configs/cases/2-data.yaml` | task from an API → isolated networkless analysis → **report invariants as the anchor** → publish the result over the API |
| 3. Automation | `configs/cases/3-automation.yaml` | file queue → bounded script (256 MB, 30 s CPU, no network) → **effect check as the anchor** → notification |

All three are verified by the test suite: if an example YAML stops loading, or
loses one of its three templates, the tests fail.

---

## Usage

```bash
export STARLIGHT_LLM_API_KEY=sk-...        # never the key in the YAML

# normal run, with the configured source
starlight -config configs/cases/1-development.yaml

# a single task, without touching the configuration
starlight -config configs/cases/1-development.yaml -task "fix TestFoo"

# the contents of a file as the task
starlight -config configs/cases/2-data.yaml -task-file task.md

# validate the configuration without calling the LLM
starlight -config my.yaml -validate-config

# see the isolation actually available on this machine
starlight -config my.yaml -isolation

# read-only plan mode: ask a single question and get a plain-text plan
starlight -p "list the .go files and suggest a refactor"
```

![Plan mode](docs/screenshots/plan.png)

Plan mode is **structurally read-only**: the agent calls tools (`read_file`,
`execute_command`) through a path that never invokes a shell, so redirections,
pipes and shell metacharacters are syntactically impossible. Destructive
commands are refused before they run.

**Graceful shutdown:** `Ctrl+C` (or `SIGINT`/`SIGTERM`) works everywhere. The first
signal cancels the current work in progress, returns from the TUI or `-init`
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
jq 'select(.level=="error")' workspace/starlight.log
jq -r 'select(.msg=="task completed") | .task' workspace/starlight.log
```

`log_max_mb` and `log_backups` control the rotation (`starlight.log.1`, `.2`, …).

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

MIT — see [LICENSE](LICENSE).
