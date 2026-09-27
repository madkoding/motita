# Calling an HTTP API with curl

Use when a task depends on a service: fetching a version or a record, reading JSON into a field, or
posting a result somewhere. The traps below are the ones that turn a working call into a silent
wrong answer.

## Know your mode first, because curl is not always available

`curl` and `wget` are classified as **writers by name** — they write files and reach the network. So:

- **Plan mode (read-only): refused**, always. There is no phrasing that gets a request through, and
  there is no shell to hide a pipe in. Do not plan a step around an API call in plan mode; say what
  you cannot reach and why.
- **Task mode: the user is asked once per line** (`external-effect`). Approved, it runs normally.

Because it costs a confirmation, **make one call do all the work** rather than five calls that each
fetch a little. `curl` accepts several URLs and concatenates the responses to stdout in order, so
two lookups are one line, no `;`, no `&&`, no loop:

```sh
curl -s -f -o - "https://host/a" "https://host/b"
```

Two JSON documents in one stream are NOT one JSON document. If something downstream needs a single
document, write the responses to separate files instead (two `-o` targets — still no shell
metacharacter).

## `-f`, or you will read the error body as data

**`curl` exits `0` on an HTTP error and hands you the error page as if it were the answer.** Measured:
a `404` exits `22` with `-f` and `0` without it. So without `-f`, a typo in a URL becomes
`{"status":404,...}` in the middle of your results, and a tired reader takes it for a field.

Always:

```sh
curl -s -f -m 20 -o /tmp/answer.json "https://api.example.com/thing"
```

- `-s` — no progress meter to mix into the output.
- `-f` — a non-2xx exit is non-zero, so the failure is visible.
- `-m 20` — a timeout. A hanging request is a hung task, and nothing else bounds it for you.
- `-o file` — keep it out of the context unless you need it there.

Then **check the exit and quote the status** when it fails. "could not fetch" is a non-answer; the
status code is the diagnosis.

## `--compressed`

Servers that gzip will do so when asked, and the byte saving is large. Measured on one API response:
**20 118 bytes plain, 5 009 with `--compressed`** — 4× less, which matters directly because output is
capped (see below). Add it to every API call that returns a document.

## The output is capped, so keep the response small on the SERVER's side

Two caps: the sandbox captures up to `sandbox.max_output_kb` (256 KiB by default), and what reaches
the model is cut at 64 KiB with a truncation banner. **The cut does not land at a record boundary** —
it falls wherever the byte budget ends, so a truncated JSON document is a prefix, not a document.

Measured: `https://pypi.org/pypi/<package>/json` is **192 KB for one ordinary package**, three times
the model-facing cap. Never let a response like that arrive whole:

- **Write to a file, then read the field with a reader.** Two calls, and neither is a write to
  anywhere the user cares about:
  ```sh
  curl -s -f -m 20 -o /tmp/answer.json "https://api.example.com/thing"
  grep -o '"version":"[^"]*"' /tmp/answer.json
  ```
- **Ask for the endpoint that returns the little thing.** A per-resource endpoint
  (`/repos/<owner>/<repo>`) beats a search that returns fifty of them.
- **`grep -o` is the poor man's JSON reader** and it is a reader, so it survives plan-mode terms and
  never needs a confirmation. `jq` is on the reader list but is **not installed on every machine**;
  `python3` is installed but is a *writer* (refused in plan mode, a hard refuse under strict). Reach
  for `grep` first.

## Method and body

- A `POST` wants `-X POST` (or `--data` / `--json`, which imply it), `-H 'Content-Type: application/json'`,
  and `-d '<json>'`. Quote the body; a shell will eat the braces otherwise.
- **`-d` on a GET silently converts it to POST.** If you meant a GET, do not pass a body.
- For a header you have to read from a file, `-H @file` is not a thing — `-H "$(cat file)"` is, and
  it costs a command substitution, which is a shell line and therefore a confirmation.
- `--data-urlencode 'q=a b c'` is how a value with spaces or `&` goes into a query string without
  becoming a different request.

## Reading a response that is not JSON

- **`w3m -dump` turns HTML into text** but it is *unclassified*: a confirmation in task mode, refused
  in plan mode. `r.jina.ai` does the same job through a plain `curl` when the page must be read.
- **A redirect is not followed by default.** `-L` follows it. Without it, a `301`/`302` means you
  fetched the redirect page, and the body is a sentence about where to go.
- **`-I` (or `-i`) is how to see the headers**, and for some services the headers ARE the answer —
  a rate limit, a deprecation notice, a content type.

## Rate limits and auth, which change what "working" means

- **Unauthenticated limits are low.** Measured: GitHub's search API allows **10 requests a minute**
  (`x-ratelimit-limit: 10`), and its rate-limit headers are visible with `-D -`. A loop over a search
  endpoint exhausts the budget and then answers `403` for everything — including calls that would
  otherwise work.
- **Some endpoints require a token and say so with `401`.** Measured: GitHub's *code* search answers
  `401` without a credential while the *repository* search works. Do not retry a `401`; report that
  the credential is missing.
- **Never invent or replay a credential.** If a call needs one, say so. A `401`/`403` is the answer to
  report, not a puzzle to solve by trying variants.

## Local APIs, which is where the traps are different

- **`localhost` and `127.0.0.1` are not always interchangeable**: a service bound to `::1` only, or
  bound with a Host-header check, answers differently to each. If one fails, try the other before
  concluding the service is down.
- **A health endpoint is not a readiness endpoint.** A process can be listening and still refuse real
  requests for a while. Prefer the endpoint that exercises the thing you depend on.
- **A `2xx` is not "the effect happened".** Verify by reading back what should have changed: the file,
  the record, the list. This is the same rule as everywhere else, and it applies most to `POST`.

## Report the call

Give the method, the URL and what came back, including the status. "Got the version from the PyPI
JSON for `requests`" is checkable; "the version is 2.x" is not. And if you could not call — plan mode,
a refusal, a timeout — say exactly that rather than answering from memory as if you had fetched it.
