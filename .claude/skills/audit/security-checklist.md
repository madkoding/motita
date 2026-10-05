# Security checklist (motita-specific)

Tick each item against the code in scope; every "no" with attacker reach becomes a finding.

## A. Network-facing gateway / Web UI (`internal/gateway`, `internal/webui`, `web/`)
- Listener bind address default (loopback vs `0.0.0.0`); is exposure explicit and warned?
- Authentication on **every** route, WebSocket/SSE upgrade and static/artifact/preview endpoint; token compare is constant-time (`subtle.ConstantTimeCompare`); tokens not in URLs/logs; token generation uses `crypto/rand`; rotation/expiry.
- CSRF and cross-origin: `Origin`/`Host` validation on state-changing and WS endpoints (DNS rebinding against localhost services), CORS not `*` with credentials, cookie flags (`HttpOnly`, `Secure`, `SameSite`).
- Security headers (CSP, `X-Content-Type-Options`, `frame-ancestors`); `http.Server` timeouts (Slowloris), `MaxBytesReader` on bodies, rate limiting/lockout on auth.
- XSS in the React UI: `dangerouslySetInnerHTML`, markdown rendering of model/repo output (sanitized?), `href`/`src` with `javascript:` URLs, HTML in reports/artifacts/previews served from same origin (stored XSS → full agent takeover).
- IDOR/authorization: can one project/session read or act on another (`projectscope`, `branchisolation`)? Mass assignment in JSON decoding (`DisallowUnknownFields`).
- Artifact/preview/file serving: path traversal, symlink following, content-type sniffing, serving files outside project root, directory listing.
- Verbose errors / stack traces / debug endpoints / pprof exposed.

## B. Command execution & sandbox (`internal/sandbox`, `execx`, `policy`, `netrules`, `readonly`)
- Shell injection: `sh -c` with concatenated strings; argument injection (values starting with `-`, missing `--`); env var injection (`LD_PRELOAD`, `GIT_*`, `PATH`); `exec.Command` resolved via attacker-writable `PATH`/cwd.
- Sandbox escape: are limits (memory/CPU/time/no-network) enforced by the kernel (rlimits, cgroups, namespaces, seccomp) or only advisory? Fail-open when unsupported? Child processes/daemons outliving the limit? `/proc`, `/dev`, mounts, symlinks, hardlinks, `chdir` tricks leaving the working dir.
- Policy/allow-list bypass: command parsing differences (quoting, `$()`, backticks, `;`, `&&`, newlines, aliases, env prefix, absolute vs relative binary, `git -c core.sshCommand=…`, `git -c core.hooksPath`, `--upload-pack`, `-exec`, interpreters like `python -c`/`node -e` to bypass read-only or network rules).
- Network rules: IPv6, IPv4-mapped, decimal/octal/hex IPs, redirects, DNS rebinding, proxy env vars, link-local/metadata (`169.254.169.254`), `localhost` aliases.
- Read-only mode: every write path (tools, git, redirects, temp files) honors it; TOCTOU.
- Resource exhaustion: output size caps, process count, disk usage, log growth.

## C. Prompt injection & agent trust (`internal/agent`, `llm`, `task`, `review`, `curator`)
- Model output used as a path, command, URL, branch name, file name or config without validation.
- Repo content (README, code comments, skills, issues, tool output, web pages) reaching the model with tool authority → exfiltration of secrets/env/files, malicious commands, auto-approve abuse (`autoapprove`).
- Can the model alter its own checks/anchor, policy, hooks, `.motita/` config, or mark PASS? Is the anchor command itself attacker-influenced (repo-controlled `anchor` config executed with user privileges)?
- Confirmation/approval flows enforced server-side, not just in the UI; destructive actions (delete, force push, reset) gated.
- Secrets in prompts/context/logs/reports/telemetry/usage records sent to providers.

## D. Secrets, OAuth, providers (`oauth`, `llm`, `config`, `usage`, `logx`)
- Credential storage: file perms (0600/0700), keyring vs plaintext, token files readable by sandboxed commands (is the credential dir inside/near the agent's reach?).
- OAuth: PKCE, `state` validation, redirect URI binding to loopback, device-flow polling limits, token refresh race/leak, scopes minimal, tokens never logged.
- TLS: no `InsecureSkipVerify`, min version, custom `Transport` honoring proxies safely, cert pinning where claimed.
- Config parsing (`configs/*.yaml`): env expansion injection, unsafe defaults, secrets in examples, committed `.env`/keys (`git log -p -S`, secret scan).
- Log redaction: Authorization headers, API keys, URLs with credentials (`https://user:token@…` git remotes).

## E. Git & content from untrusted repos (`gitx`, `gitforge`, `skills`, `projectskills`, `template`)
- Clone/fetch of attacker-controlled URLs: `ext::`/`file://`/`ssh -o ProxyCommand` option injection, submodule URLs, `core.fsmonitor`/hooks/`.gitattributes` filters executed on checkout, credential helper leakage, redirect to internal hosts (SSRF).
- Branch/ref/path names: validate against `git check-ref-format`, leading `-`, `..`, NUL, newline; worktree/branch isolation can't be escaped.
- Skills/templates: zip-slip/tar traversal on extract, symlinks in archives, `text/template` vs `html/template` injection, executable skills auto-run from a freshly cloned repo without consent.
- Path handling everywhere: `filepath.Clean` then `HasPrefix` (wrong without separator), `filepath.Join` with absolute user input, symlink resolution (`EvalSymlinks`) before containment checks, Windows paths/UNC/8.3 names, case-insensitive FS.

## F. Updater, install, release, supply chain (`updater`, `scripts/install.*`, `.github`, `Makefile`)
- Update/install integrity: signature or at least checksum verified from a trusted, separate channel; HTTPS enforced; no downgrade; atomic replace with safe perms; version/asset names not used as paths unsanitized; rollback.
- `curl | sh` installers: quoting, temp file handling (predictable names, `/tmp` races), `set -euo pipefail`, PowerShell execution policy/`iex` of remote content.
- GitHub Actions: `pull_request_target`/`workflow_run` with untrusted checkout, script injection via `${{ github.event.* }}` in `run:`, unpinned third-party actions (pin by SHA), broad `permissions:` (default to read), secrets exposed to forks, cache poisoning, release token scope, `.releaserc.json` / semantic-release plugins, `CODEOWNERS` covering workflows & scripts.
- Dependencies: `govulncheck`, `npm audit`, typosquats, `postinstall` scripts, lockfile integrity, `go.mod` `replace` directives, unpinned `go run pkg@latest`.
- Git hooks (`.githooks`) can't be a vector for contributors/CI; no secrets baked in binaries/site.

## G. Go-specific pitfalls
- `math/rand` for security; `==` on secrets; `unsafe`/`reflect`/`cgo`; `os/exec` with user data; `http.DefaultClient` without timeout; unbounded `io.ReadAll`; `defer` in loops; goroutine/ctx leaks; `json.Unmarshal` into `interface{}` then type-assert panic (remote DoS via panic — missing `recover` in handlers/goroutines); integer conversion overflow in sizes/offsets; `regexp` on huge input (RE2 is safe from ReDoS but not from size); `filepath.Walk` following into symlinks; `template` parse of user input; temp files via `os.Create` in shared dirs instead of `os.CreateTemp`/0600; file permission masks (`0644`/`0755` on secret or state files); `time.After` leaks.

## H. Web/TypeScript pitfalls (`web/`, `site/`)
- Untrusted data into `innerHTML`/markdown/`iframe srcdoc`; `postMessage` without origin check; secrets in `localStorage`/bundle/source maps; `target=_blank` without `rel=noopener`; unvalidated redirects; third-party scripts without SRI; build output embedding (`go:embed`) shipping dev/test endpoints.

## I. Data & privacy
- Persistent state (sessions, reports, screenshots, usage) may hold secrets/PII: location, perms, retention, deletion truly deletes, export/share paths.
