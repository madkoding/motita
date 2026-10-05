---
name: audit
description: Deep code audit of motita (general quality + security/cybersecurity). Finds fine-grained and critical vulnerabilities, traces how each could be exploited, and reports prioritized findings with concrete remediation. Use when asked to audit, review security, hunt vulnerabilities, or assess exploitability of the repo, a package, a diff or a PR.
argument-hint: "[scope: path | package | PR | 'full'] [mode: general | security | all (default all)]"
---

# Audit skill

Read-only audit. **Never modify code, never run exploits against real systems, never print secret values.**
Output is a report; fixes are only *proposed* unless the user asks to apply them.

## 1. Scope & mode

Parse `$ARGUMENTS`:
- scope: a path/package (`internal/gateway`), a PR/diff (`git diff origin/main...HEAD`), or `full` (default).
- mode: `general`, `security`, or `all` (default).

For `full`, split by trust boundary and fan out parallel subagents (one per area, each told to read
`security-checklist.md` and report in the finding format below). Suggested split:
1. `internal/gateway`, `internal/webui`, `web/` — network-facing surface, auth, HTTP, WebSocket/SSE, XSS/CSRF.
2. `internal/sandbox`, `internal/execx`, `internal/policy`, `internal/netrules`, `internal/readonly` — isolation & command execution.
3. `internal/oauth`, `internal/llm`, `internal/config`, `internal/usage`, `internal/logx` — secrets, tokens, provider calls.
4. `internal/gitx`, `internal/gitforge`, `internal/projectstore`-like code, `internal/skills`, `internal/projectskills`, `internal/template` — untrusted repos/content, path handling.
5. `internal/updater`, `scripts/install.*`, `.github/workflows`, `Makefile`, `.githooks`, `go.mod`, `package*.json` — supply chain & release integrity.
6. Everything else (`agent`, `session`, `task`, `schedule`, `curator`, `review`, `tui`, …) — logic, concurrency, resource limits.

## 2. Reuse motita's own procedures (read first)

motita ships its practices as embedded procedures in `internal/skills/builtin/*.md`. They are the
project's source of truth; read the ones below before auditing and cite them by name instead of
restating them (they change, a copy rots):

| Procedure | Use it for |
| --- | --- |
| `auditing-code.md`, `finding-vulnerabilities.md` | The method, report format, severity scale and generic vulnerability checklist motita's own agent uses. They are the shared source: follow them, and treat `security-checklist.md` here as the motita-specific addendum on top. |
| `verifying-a-change.md` | The evidence standard for every finding: a claim needs a second read that observes the effect, not "the code looks wrong". Reproduce the failure before claiming a fix; flag tests that pass for the wrong reason; quote counts with their denominator. |
| `running-commands.md` | How to run tooling (`go vet`, `govulncheck`, `-race`, `npm audit`) and read exit codes/timeouts correctly. |
| `git-in-a-repository.md` | Safe git use while auditing history/diffs, and when applying fixes. |
| `pull-requests-and-ci.md` | Semantic commits/PR titles and driving CI to green when fixes are requested. |
| `files-and-directories.md`, `calling-an-http-api.md`, `installing-a-toolchain.md` | Only when the audited area touches them (path handling, HTTP clients, toolchain install) — compare the code against what they prescribe. |

Also read `CONTRIBUTING.md` (the 8 CI rules: 100% coverage, `-race`, fmt, vet, no Spanish in code,
cross-platform build, <20 MB binary, staticcheck+govulncheck). **Any violation of a procedure or rule
is a general-quality finding that cites the rule**. Conversely, code that bypasses a guard these
procedures prescribe (e.g. a non-semantic-commit refusal, a "fake PASS") is a security-relevant finding.

## 3. Method (do all of it, don't stop at a grep)

1. **Map the attack surface**: entry points (HTTP routes, CLI flags, files read, env, stdin, git remotes, LLM output, skills/templates, update feed), trust boundaries, and what each can reach (fs, exec, network, secrets).
2. **Treat these as untrusted input**: HTTP requests, repository contents (including `.motita/`, skills, templates, hooks), LLM/model output and tool calls (prompt injection), remote git data, update manifests, filenames, env of child processes.
3. **Trace data flow source → sink** by reading code, not just matching patterns. A finding needs: attacker-controlled source, the path it takes, the sink, and why existing guards fail.
4. **Run available tooling when it exists offline** (report if skipped): `go vet ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`, `staticcheck` (see `Makefile`), `go test -race ./...` for the scoped packages, `npm audit --omit=dev` in `web/` and root, `git log -p -S` / `grep` for committed secrets. Tooling output is a lead, never a finding by itself.
5. **Verify** each candidate following `verifying-a-change.md`: re-read the code, look for the guard you may have missed, check tests that assert the behavior. Drop or downgrade anything you can't substantiate. Mark confidence honestly.
6. **Chain** findings: low-severity issues that combine into a higher-severity exploit (e.g. path traversal + writable skill dir + auto-run = RCE) get reported as a chain.
7. Check what's **already done well** so the report isn't only negatives.

## 4. General audit dimensions (mode general/all)

- Correctness & logic: error handling swallowed, nil derefs, off-by-one, wrong state transitions, "fake PASS" paths (this project's core promise: only the anchor declares PASS — flag any path where a model-proposed result is accepted without a real check).
- Concurrency: data races, goroutine leaks, missing `context` cancellation, unbounded channels, lock ordering, TOCTOU.
- Resource handling: unclosed files/bodies, unbounded reads/memory/output, missing timeouts, no limits on queues/retries (matters on the 484 MB target).
- API & design: leaky abstractions, duplicated logic, dead code, exported surface, config defaults that are unsafe.
- Tests & CI: 100% coverage rule (`CONTRIBUTING.md`), tests that assert nothing, missing negative/security tests, flaky patterns.
- Maintainability, docs drift, i18n, accessibility/UX issues in `web/` where they hide bugs.
- Dependencies: outdated/unmaintained/abandoned, license issues, unpinned tool versions.

## 5. Security audit (mode security/all)

Follow `security-checklist.md` in this directory category by category. Go beyond the obvious: look for
subtle bypasses (symlink races, Unicode/case normalization, argument injection via `--flag`-like values,
`..` after cleaning, host/URL parsing differentials, DNS rebinding, redirects to internal addresses,
non-constant-time compares, ReDoS, integer overflow in size math, TOCTOU between check and use,
logging of secrets, error messages leaking paths/tokens, default-open listeners).

For each real vulnerability, include a **plausible exploit scenario** (attacker, precondition, steps,
impact) at the level needed to understand and fix it — a short description or minimal input, **not a
weaponized exploit** and never run against anything but local test fixtures.

## 6. Report format

Write to `audit-report.md` in the scratchpad dir (or the path the user gives); do not commit it unless asked.
Chat reply = brief bullet summary + path + top 3 actions.

```
# Audit report — <scope> — <date> — <commit sha>
## Summary
- counts by severity; overall posture (1–3 sentences); what was NOT covered and why
## Findings (ordered by severity, then confidence)
### [ID] <title>
- **Severity**: Critical | High | Medium | Low | Info   (CVSS-style reasoning in one line: AV/AC/PR/UI/impact)
- **Category**: CWE-### / OWASP / general-quality dimension
- **Confidence**: High | Medium | Low (+ why)
- **Location**: `path/file.go:line` (list all relevant)
- **Description**: what is wrong, in this code
- **Exploit scenario**: attacker → precondition → steps → impact (security findings)
- **Evidence**: the code excerpt / trace / tool output (secrets redacted)
- **Remediation**: concrete fix, with a code/diff snippet when short; the safest option first
- **Regression test**: the test that would have caught it (project demands 100% coverage)
- **Effort / risk of fix**: S/M/L, breaking-change notes
## Attack chains
## Positive observations
## Hardening suggestions (defense in depth, not tied to a finding)
## Tooling run (command → result/skipped)
```

Severity guide: **Critical** = unauthenticated/remote or sandbox-escape code exec, secret/token theft, auth bypass,
supply-chain takeover. **High** = authenticated priv-esc, arbitrary file read/write outside project, SSRF to
internal/metadata, stored XSS in the web UI. **Medium** = needs unusual preconditions, DoS, info leak, weak
defaults. **Low/Info** = hardening, hygiene.

## 7. Rules

- Be precise and terse; no padding, no generic advice ("validate input") without the exact location and fix.
- No false-positive spam: prefer 8 verified findings over 40 guesses. Put unverified leads in a separate "Needs confirmation" list.
- Never exfiltrate or echo real secrets; show `AKIA…[redacted]` style. If a live secret is found, say it must be rotated.
- Security findings of Critical/High: tell the user in chat immediately, and recommend private disclosure rather than a public issue/PR comment.
- If asked to fix: follow `pull-requests-and-ci.md` and `verifying-a-change.md` — one semantic commit per finding (`fix(security): …`), a regression test that fails without the fix, `make check` green.
