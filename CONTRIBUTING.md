# Contributing to motita

Thanks for wanting to contribute. This document is the contract between
you and the repository — read it before opening a PR.

## TL;DR

```bash
git clone git@github.com:madkoding/motita.git
cd motita
make check     # fmt-check + vet + test
make cover     # 100% coverage per package
make build     # binary builds for host
```

If **all three** pass, CI will almost certainly pass too. If any of them
fails, CI will fail — fix it before pushing.

## The rules

These are not suggestions. CI enforces every one of them, and a PR that
breaks any rule will not be merged.

### 1. 100% test coverage — non-negotiable

Every package in `./internal/...`, `./cmd/...` and `./tools/...` must be
at **100.0%** coverage. The gate checks package by package, so a gap in
one package cannot hide behind a high aggregate.

```bash
make cover
```

If you add code, add the tests that cover it. If you add a new package,
add tests for it — a package with no test files gets a warning, not a
pass.

**Test-support packages** (packages that import `"testing"` in non-test
source, like `tools/clitest`) are exempt: their remaining uncovered
statements are their own failure and skip branches, which only run when
a test has already failed. The exemption is derived from a property
(presence of `"testing"` in production source), not a hardcoded list,
so it cannot be widened by adding a name.

### 2. All tests pass — with the race detector

```bash
make test        # go test -count=1 ./...
```

CI also runs `go test -count=1 -race ./...`. If your code has a data
race, the race detector will find it.

### 3. Code is formatted

```bash
make fmt         # gofmt ./...
make fmt-check   # fails if any file is unformatted
```

### 4. `go vet` is clean

```bash
make vet
```

### 5. No Spanish in code, configs, or scripts

The project is English-only in its codebase. `scripts/verify.sh` step 6
scans all tracked `.go`, `.yaml`, `.sh`, `.md` and `Makefile` files for
Spanish text and fails if any is found.

If you need Spanish in a test fixture (the `cleanAssumption` feature,
accented test data, etc.), mark the line with `spanish-fixture:` so the
gate exempts it deliberately.

### 6. The binary builds for every supported platform

motita targets 9 platform/architecture combinations (see `PLATFORMS` in
the Makefile). CI builds and smoke-tests all of them. If you add a
platform-specific code path, verify it compiles:

```bash
make test-matrix   # builds tests for every platform
```

### 7. The binary stays under 20 MB

CI rejects any binary over 20 MB. The ceiling guards against runaway
growth — a dependency that drags a framework in — rather than capping
features. If you add a dependency (the project currently has **zero**
external dependencies — only the Go standard library), check the binary
size impact.

### 8. `staticcheck` and `govulncheck` pass

CI runs both. They are pinned to specific versions to avoid a green
build turning red overnight when upstream adds a check.

## The full local gate

Before pushing, run the complete verification pipeline — the same one CI
runs, plus a few checks CI cannot do locally:

```bash
./scripts/verify.sh
```

This runs, in order:

1. `gofmt` — no unformatted file
2. `go build` — everything compiles
3. `go vet` — no static-analysis findings
4. `go test -race` — every test green, with the race detector
5. Coverage — per package and aggregate, against the 100% gate
6. English check — no Spanish left in code, configs or scripts
7. POSIX sh — the installer script parses as POSIX sh
8. E2E tests — on every linux architecture
9. Web UI E2E — the browser interface over HTTP

If `verify.sh` passes locally, CI will pass.

## PR process

1. **Fork** the repository (or create a branch if you have push access).
2. **Create a branch** from `main`: `git checkout -b feature/my-feature`.
3. **Write code + tests**. Coverage must be 100% for the packages you
   touched.
4. **Run the gates locally**: `make check && make cover && make build`.
5. **Open a PR** against `main`. The PR template has a checklist — fill
   it in.
6. **CI runs automatically** on every push. If it fails, fix the issue
   and push again.
7. **Review**: a code owner (`@madkoding`) must approve before merge.
8. **Squash-merge** is preferred — one commit per PR.

## What CI does (so you know what to expect)

The CI pipeline (`.github/workflows/ci.yml`) runs on every push to
`main`, every tag, and every pull request:

| Job | What it does |
|-----|-------------|
| **verify** | `gofmt`, `go vet`, `go test -race`, `staticcheck`, `govulncheck`, Go floor check, 100% coverage gate per package |
| **build** (9 platforms) | Cross-compiles for every supported OS/arch, verifies the binary format (ELF/PE/Mach-O), runs smoke tests in containers, validates config without an LLM key, runs E2E tests |
| **gateway** | HTTP E2E test of the gateway + binary size ceiling |
| **release** | Only on tags (`v*`): publishes binaries + SHA256SUMS to GitHub Releases |

A PR must have **all required jobs green** before it can be merged.

## Branch protection

The `main` branch is protected:

- PRs are required (no direct pushes to `main`)
- CI must pass before merge
- A code owner review is required
- Force pushes to `main` are blocked

If you have push access and need to bypass a check, explain why in the
PR — the bypass is a record, not a shortcut.

## Code style

- **Go**: follow `gofmt` and `go vet`. Use `golint`-style doc comments
  on exported identifiers. Keep functions short and focused.
- **No external dependencies**: the project builds with the Go standard
  library only. Adding a dependency requires justification and a size
  impact check.
- **English only**: code, comments, configs, commit messages and PR
  descriptions are in English.
- **Commit messages**: use
  [conventional commits](https://www.conventionalcommits.org/) (`feat:`,
  `fix:`, `docs:`, `test:`, `refactor:`, `chore:`).

## Questions?

Open an issue with the `question` label, or start a discussion.