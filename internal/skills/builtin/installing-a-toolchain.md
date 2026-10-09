# Installing a toolchain the project needs

Use when a check or a build fails with "command not found" for a language toolchain or a build
tool (go, node, npm, gh, make, cargo, python packages), on a machine where it is not installed.
Installing is a ONE-TIME cost: done right, the next round and the next session find it ready.

## First, make sure it is really missing

1. `command -v go node npm gh make` — the sandbox PATH already includes the tools directory's
   `bin/` and every `tools/<name>/bin/`, so a toolchain installed in an earlier session is found.
2. `ls <tools dir>/tools` — the prompt's TOOLS AND HOME section names the tools directory.
3. Only what is still missing gets installed. Never reinstall something that answers to
   `command -v`.

## Where things go

- Unpack a toolchain to `<tools dir>/tools/<name>/`, so its binaries end up in
  `<tools dir>/tools/<name>/bin/`. That directory is on PATH from the next command on.
- A single binary goes in `<tools dir>/bin/`.
- Every project's sessions share those directories, so only a command the user approved can
  write there; the same command run unasked is refused by the sandbox. Put the unpacking and
  the linking in that approved command, not in a script that runs later.
- Never into the repository, never into `/usr/local` or another system directory, never with
  `sudo`. Do not `export PATH=...` in your commands: the PATH is already set for you.
- HOME is outside the repository too, so caches (`~/go`, `~/.npm`, `~/.cache`) never land in a
  commit.

## Official sources, one command each

Download to the temporary directory (`$TMPDIR`), extract, and replace any previous copy:

- **Go**: the latest version is the first entry of `https://go.dev/dl/?mode=json`;
  the tarball is `https://go.dev/dl/<version>.linux-amd64.tar.gz`, and it already contains a
  `go/` folder: `tar -C <tools dir>/tools -xzf go.tgz` gives `tools/go/bin/go`.
- **Node.js**: the first entry of `https://nodejs.org/dist/index.json` whose `lts` is not false;
  the tarball is `https://nodejs.org/dist/<v>/node-<v>-linux-x64.tar.xz`. Extract with
  `--strip-components=1` into `tools/node/`.
- **GitHub CLI**: the tag of `https://api.github.com/repos/cli/cli/releases/latest`; the tarball
  is `gh_<version>_linux_amd64.tar.gz` from that release. Extract with `--strip-components=1`
  into `tools/gh/`.
- **make** (and other small Debian packages) without root: `apt-get download make`, then
  `dpkg -x make_*.deb <tools dir>/tools/make`, then link `tools/make/usr/bin/make` into
  `<tools dir>/bin/make` with an ABSOLUTE target, so moving nothing ever breaks it.

Pick the archive that matches `uname -m` (x86_64 is amd64/x64, aarch64 is arm64).

## Then verify, in the NEXT command

`go version; node -v; gh --version | head -1; make --version | head -1` — without exporting
anything. If one is still not found, the archive was extracted one level too deep or too
shallow: `ls <tools dir>/tools/<name>` shows where its `bin/` ended up.

## After the toolchain: the project's own dependencies

Install them the way the project does (`npm ci`, `go mod download`, `pip install -r ...`), once,
inside the working directory, and run the project's checks.
