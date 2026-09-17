# starlight 🌟

A command-line AI agent written in **pure Go** (standard library only, **zero
external dependencies**) that talks to any OpenAI-compatible API and can read
files and run commands on the machine it runs on.

Built to work on **i386 machines (linux/386)**: the binary is static, does not
use `cgo`, drags in no C libraries and applies explicit memory and time limits so
it never overruns a 32-bit machine.

```
$ starlight
🤖 Starlight ready. Type your instruction ('/help' for help, 'exit' to quit).
   model=gpt-4o-mini  endpoint=https://api.openai.com/v1  max-loops=5

> check the free disk space and tell me if anything looks off
  [🧠 Thinking...]
  [⚙️  Running tool: run_command]
  [🧠 Thinking...]
/ is 62% full (18G free) and /var/log weighs 1.2G; nothing unusual.
```

## Installation

### Cross-compiling for i386 from a modern machine

With Go installed, you do not need to compile on the 32-bit machine:

```bash
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o starlight-linux-386 ./cmd/chat
# or, with the Makefile:
make 386          # builds and verifies the ELF is 32-bit
make all          # 386 + amd64 + arm64
```

The result is a static binary of about **6.6 MB (6.3 MiB) with Go 1.27**
(ELFCLASS32) that you copy to the target machine over `scp`, `ftp` or USB:

```bash
chmod +x starlight-linux-386
./starlight-linux-386
```

### Building on the i386 machine itself

```bash
cd starlight && make 386     # requires Go 1.23 or newer
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `OPENAI_API_KEY` | yes | — | API key. |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | Works with OpenRouter, LocalAI, vLLM, Ollama, LiteLLM… |
| `OPENAI_MODEL` | no | `gpt-4o-mini` | Model to use. |
| `NO_COLOR` | no | — | If set, disables the ANSI colours. |

```bash
export OPENAI_API_KEY="your_key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_MODEL="gpt-4o-mini"
./starlight-linux-386
```

Everything can also be passed as flags: `-model`, `-url`, `-max-loops`,
`-timeout`, `-no-color`, `-version`.

## Usage

```bash
starlight                                   # interactive REPL
starlight -p "list the .go files in the current directory"    # one instruction and exit
starlight "summarise the README and tell me if anything is missing"   # equivalent
```

REPL commands: `/help`, `/clear` (forgets the conversation) and `exit`
(`Ctrl+D` works too).

The traces (`[🧠 Thinking...]`, `[⚙️  Running tool: ...]`) go to **stderr** and the
final answer to **stdout**, so it can be piped:

```bash
starlight -p "tell me the kernel version" > summary.txt
```

## Agent tools

| Tool | Arguments | What it does |
|---|---|---|
| `read_file` | `path` | Returns the contents of a text file. Refuses directories and files over **1 MiB**. |
| `run_command` | `cmd`, `timeout_seconds` (optional) | Runs the command through `sh -c` (pipes and redirections work) and returns stdout and stderr combined, truncated to **64 KiB**. Defaults to a 120 s limit. |

The agent loop allows up to **5 tool-calling iterations** per turn
(`-max-loops`); once they are used up it asks for the final answer with
`tool_choice: "none"`, so it never hangs in an infinite loop.

## Design decisions (and why)

- **Zero dependencies.** Guarantees the 32-bit cross-compilation works with no
  fights against `cgo` or missing C libraries.
- **`sh -c` for commands.** Allows pipes, redirections and quotes exactly as a
  person would use them in a terminal.
- **1 MiB read limit.** Stops the agent from trying to load a video or a giant
  log and exhausting RAM, which is scarce on an i386.
- **Process group killed when the deadline expires.** `exec.CommandContext` on
  its own kills `sh`, but its children (`sleep`, scripts, pipes) stay alive
  holding the pipe open and `Wait` keeps waiting: measured, a `sleep 30` with a
  1 s limit took **5 s** to return. With `Setpgid` plus a `SIGKILL` to the group
  it cuts at the exact deadline. Covered by `TestRunCommandTimeout`.
- **Normalising `arguments`.** The OpenAI spec sends a tool's arguments as a
  **string** containing JSON; several "compatible" gateways send an object.
  `decodeArgs` accepts both (and empty/`null`) instead of failing with
  `cannot unmarshal string`.
- **History bounded to 41 messages** keeping the system prompt and never leaving
  an orphaned tool result (some providers reject a `tool` message without its
  preceding `assistant`).

## Verification

```bash
make check        # gofmt + go vet + go test  (what CI runs)
./scripts/e2e-i386.sh    # end-to-end test in a real i386 container
```

- **17 test cases** (`go test -race ./...`) over the tools, the HTTP client, the
  agent loop, the history trimming and the configuration.
- The CI (`.github/workflows/ci.yml`) runs `gofmt`, `vet` and `test -race`,
  builds `linux/386`, `linux/amd64` and `linux/arm64`, **verifies the i386
  binary is ELFCLASS32** (reading the ELF header and with `file`), runs the
  binary inside an `i386/debian` container and, when a `v*` tag is published,
  uploads the binaries to a release.

### End-to-end test

`scripts/e2e-i386.sh` builds `tools/mockapi` and the chat for 386, and inside a
`--platform linux/386` container it starts the mock, lets the agent run real
tools and checks the marker in the final answer:

```
==> Running inside i386/debian:bookworm-slim (--platform linux/386)
  [🧠 Thinking...]
  [⚙️  Running tool: run_command]
  [⚙️  Running tool: read_file]
  [🧠 Thinking...]
architecture: i386
E2E-RESULT run_command=x86_64 32 | read_file=PRETTY_NAME="Debian GNU/Linux 12 (bookworm)" ...

E2E i386 OK: the 32-bit binary ran real tools and closed the agent loop.
```

`tools/mockapi` implements the script of a model that asks for two tools and then
summarises the results; it is there to test the agent without spending tokens or
needing a key.

## Layout

```
cmd/chat/main.go             the whole chat (configuration, API, tools, REPL)
cmd/chat/process_unix.go     process group: killing the child and its descendants
cmd/chat/process_other.go    alternative for systems without POSIX process groups
cmd/chat/main_test.go        17 test cases
tools/mockapi/main.go        OpenAI-compatible server for tests
scripts/e2e-i386.sh          end-to-end test on 32-bit
Makefile                     build, test, cross-compiling
```

## Adding a tool

1. Define its arguments (`type myArgs struct { ... }`).
2. Write its `toolMyThing` function returning a `string`.
3. Add the matching `case` in `(*agent).runTool`.
4. Describe it in `toolDefinitions()` so the model knows it exists.

## License

MIT — see [LICENSE](../../LICENSE).
