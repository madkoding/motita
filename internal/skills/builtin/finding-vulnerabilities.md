# Finding vulnerabilities: trace the input to the sink and show how it breaks

Use when the task is a security review, a vulnerability hunt, or a question of whether
something can be exploited. It is the checklist behind "auditing-code": that one says how to run
and report an audit, this one says what to look for and how to think like the attacker.

## Think in sources, sinks and guards

A vulnerability is an untrusted **source**, a dangerous **sink**, and a **guard** between them
that is missing or wrong. Untrusted means: anything from the network, a repository you did not
write, a file name, an environment variable of a child process, a model's output, a downloaded
manifest. Sinks: a shell or process start, a filesystem path, a URL fetch, a query, an HTML or
template render, a deserializer, a log line, a privilege or permission decision.

For each candidate write the three out. If you cannot name the source, it is not reachable. If
you cannot name the guard and why it fails, you have not read far enough.

## Checklist, by area

**Authentication and sessions**
- A route, upgrade (WebSocket, SSE) or static endpoint with no check. Compare tokens in constant
  time; tokens come from a cryptographic generator, are not in URLs or logs, and can expire.
- Default listener on all interfaces. A local service reachable from a web page through DNS
  rebinding or a missing `Origin`/`Host` check. CORS with credentials and a wildcard. Cookie flags.
- Authorization that lives only in the UI: the server must re-check who may act on which object
  (one project or user reading another's data is an IDOR).

**Injection**
- Shell: a command assembled from strings; argument injection through a value that starts with
  `-` (use `--`); environment injection (`LD_PRELOAD`, `PATH`, `GIT_*`); `git -c` options such as
  `core.sshCommand`, `core.hooksPath`, `--upload-pack`.
- SQL and templates: concatenation instead of parameters; text templates used for HTML.
- Allow-lists defeated by parsing differences: quoting, `$()`, backticks, newlines, `;`, `&&`,
  absolute versus relative binaries, an interpreter flag (`python -c`, `node -e`) used to do what
  the list forbids.

**Paths and files**
- Traversal: `..` that survives cleaning, `Clean` then a prefix check without a separator, a join
  with an absolute user value, symlinks and hard links followed before the containment check,
  archive entries that escape on extract (zip-slip), Windows names, case-insensitive disks.
- Check-then-use races between validating a path and opening it. Predictable temp names, secret
  or state files created with loose permissions, world-readable credentials.

**Network and fetches**
- Requests to user-supplied URLs: redirects to internal hosts, metadata addresses
  (`169.254.169.254`), IPv6 and IPv4-mapped forms, decimal or hex IP spellings, DNS rebinding,
  proxy environment variables. Disabled certificate checks, old TLS, missing timeouts.
- Fetching or cloning an attacker-controlled repository: submodule and `ext::` URLs, hooks and
  filters that run on checkout, credentials leaked to a redirect.

**Web interfaces**
- Untrusted text into `innerHTML`, a markdown renderer without a sanitizer, `javascript:` links,
  `postMessage` with no origin check, secrets in browser storage or in a shipped source map,
  user content served from the same origin as the app (stored XSS becomes full takeover).
- Missing body-size limits, server timeouts and rate limits on login: cheap denial of service.

**Secrets**
- Keys or tokens committed (search history too: `git log -p -S`), in examples, in prompts, in
  logs, in reports or telemetry, in the process list. Logs that print Authorization headers or
  `https://user:token@host` URLs. Credential files inside the directory a sandboxed command can read.
- OAuth: PKCE, `state` checked, redirect bound to loopback, scopes minimal, refresh handled
  without a race that leaks or loses a token.

**Agents and models**
- Model output used as a path, command, URL, branch or config value without validation.
- Content the model reads (README, issues, web pages, tool output) that can steer it into
  exfiltrating secrets or running commands: prompt injection. Approval flows enforced only in the
  UI. A check that the model or the repository can edit, so the model can mark its own work as
  passed. Destructive actions (delete, force push, reset) not gated.

**Sandboxes and limits**
- Limits that are advisory instead of enforced by the kernel (rlimits, cgroups, namespaces,
  seccomp), or that fail open when unsupported. Children that outlive a timeout. Output, process
  count and disk use without a cap. A read-only mode with a write path around it.

**Supply chain and release**
- Updates or installers with no signature or checksum from a separate channel, no HTTPS, a
  downgrade, an asset name used as a path, a piped `curl | sh` with unquoted values.
- CI: `pull_request_target` or `workflow_run` with an untrusted checkout, `${{ github.event.* }}`
  interpolated into `run:`, third-party actions not pinned by commit, broad token permissions,
  secrets reachable from forks, cache poisoning.
- Dependencies: known advisories, typosquats, `postinstall` scripts, `replace` directives,
  tools run at `@latest`.

**Language pitfalls**
- Go: `math/rand` for secrets, `==` on secrets, `http.DefaultClient` with no timeout, unbounded
  `io.ReadAll`, a type assertion on decoded JSON that can panic with no `recover` in a handler or
  goroutine, integer conversions in size math, `os.Create` in a shared directory, `0644` on
  secrets, `defer` in loops.
- JavaScript and TypeScript: prototype pollution through merged user objects, `eval` and
  `new Function`, `child_process.exec` with strings, regexes that backtrack, `target=_blank`
  without `rel=noopener`.
- Python: `pickle`, `yaml.load`, `subprocess(shell=True)`, `assert` as a guard, `tarfile.extractall`.

## Show how it breaks, then how to fix it

For every real finding give a short exploit scenario: the attacker, what they need, the input
or request that triggers it, what they gain. A minimal request or file is enough, and it runs only
against a local test fixture. Then give the fix:

- Fix the class of bug, not the one input: parameterize, use `--`, resolve symlinks and check
  containment on the resolved path, use an allow-list of what is permitted instead of a deny-list
  of what is not.
- Prefer the fix that is hard to get wrong later (a helper every call site uses) over a comment.
- Add a regression test that carries the hostile input and fails without the fix.
- Say what the fix may break, and whether a secret that was exposed must be rotated.

## Pitfalls already paid for

- **A scanner hit is not a vulnerability.** Prove the source reaches the sink, or call it a lead.
- **The guard may be one layer up.** Before reporting, check the caller, the middleware and the
  tests; the missing check is sometimes where you did not look yet.
- **Do not stop at the first bug in a function.** Where one input was unchecked, its neighbours
  usually are too; look at every use of the same helper.
- **Severity follows reach.** A flaw only the local user can trigger on their own machine is not
  Critical; the same flaw behind an open listener is.
- **Never test a vulnerability against a live system you do not own.** Reproduce it on a copy.
