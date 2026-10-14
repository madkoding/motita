# Calling an HTTP API from PowerShell

Use when a task depends on a service: fetching a version or a record, reading JSON into a field, or
posting a result somewhere. This is the Windows version: commands run under PowerShell, where the
request is made with `Invoke-RestMethod`, `Invoke-WebRequest` or `curl.exe`. The traps below are the
ones that turn a working call into a silent wrong answer.

## Know your mode first, because a request is not always available

- **Plan mode (read-only): refused**, always. No program that reaches the network is a reader, and
  plan mode has no shell to run a cmdlet in. Do not plan a step around an API call in plan mode;
  say what you cannot reach and why.
- **Task mode: the user is asked once per line** (`external-effect`). Approved, it runs normally.

Because it costs a confirmation, **make one call do all the work** rather than five calls that each
fetch a little.

## `curl` is not curl in Windows PowerShell

In Windows PowerShell 5.1, `curl` and `wget` are aliases of `Invoke-WebRequest`: `curl -s -f URL`
fails on a parameter it does not have. Windows 10 and later ship the real curl as **`curl.exe`** —
write the `.exe` and every flag of the Linux procedure works (`-s -f -m 20 -o file --compressed`).
PowerShell 7 dropped the aliases, but `curl.exe` is the same program on both, so write it that way.

## `Invoke-RestMethod`: the error IS an error, the answer is an object

```powershell
Invoke-RestMethod -Uri 'https://api.example.com/thing' -TimeoutSec 20
```

- **A non-2xx status throws**, and the command exits non-zero: unlike curl without `-f`, an error
  page is never handed to you as data. Quote the status from the error when it fails — "could not
  fetch" is a non-answer; the status code is the diagnosis.
- **`-TimeoutSec`, always.** A hanging request is a hung task, and nothing else bounds it for you.
- **The JSON is parsed into objects**, and the console prints them as a table that cuts long
  values and nests deeper levels as `@{...}`. To read one field, select it:
  `Invoke-RestMethod -Uri $u | Select-Object -ExpandProperty version` (a `$variable` makes the line
  confirmed as a whole, so write the URL literally: `-Uri 'https://...'`).
- To see the raw JSON, use `Invoke-WebRequest` and its `Content`, or save it to a file (below).

## The output is capped, so keep the response small on the SERVER's side

Two caps: the sandbox captures up to `sandbox.max_output_kb` (256 KiB by default), and what reaches
the model is cut at 64 KiB with a truncation banner. **The cut does not land at a record boundary**,
so a truncated JSON document is a prefix, not a document. Some ordinary endpoints answer with
hundreds of kilobytes (the PyPI JSON for one package is one).

- **Write to a file, then read the field with a reader.** Two calls; the second needs no
  confirmation:
  ```powershell
  Invoke-WebRequest -Uri 'https://api.example.com/thing' -TimeoutSec 20 -OutFile answer.json
  Select-String -Path answer.json -Pattern '"version":\s*"[^"]*"'
  ```
- **Ask for the endpoint that returns the little thing.** A per-resource endpoint
  (`/repos/<owner>/<repo>`) beats a search that returns fifty of them.
- `Select-String` is the reader for a saved response: it never needs a confirmation. `jq` is a
  reader too, but it is rarely installed on Windows.
- `Invoke-WebRequest` in Windows PowerShell 5.1 parses the HTML of every response with the old
  Internet Explorer engine unless told not to: add **`-UseBasicParsing`** there (PowerShell 7
  ignores it).

## Method and body

- A `POST`: `-Method Post -ContentType 'application/json' -Body '{"name":"value"}'`. Single quotes
  keep the braces and the double quotes literal for PowerShell; inside double quotes, `$` would
  expand and the line would be confirmed as code.
- **`-Body` on a GET sends it as a query string**, not a body. If you meant a GET, do not pass one.
- Headers: `-Headers @{ Accept = 'application/json' }` is a hashtable literal, which makes the
  line confirmed as a whole; with `curl.exe` the same header is `-H 'Accept: application/json'`.
- A value with spaces or `&` in a query string: encode it (`%20`, `%26`) or use
  `curl.exe -G --data-urlencode 'q=a b c'`.

## Reading a response that is not JSON

- `Invoke-WebRequest` returns the body in `Content` and the headers in `Headers`; `curl.exe -I`
  (or `-D -`) shows the headers. For some services the headers ARE the answer — a rate limit, a
  deprecation notice, a content type.
- **Redirects are followed** by both cmdlets (up to `-MaximumRedirection`, 5 by default); `curl.exe`
  needs `-L`. Without it, a `301`/`302` means you fetched the redirect page.
- `r.jina.ai` turns a page into text through a plain request when an HTML page must be read.

## Rate limits and auth, which change what "working" means

- **Unauthenticated limits are low.** GitHub's search API allows 10 requests a minute without a
  token; a loop over a search endpoint exhausts the budget and then answers `403` for everything.
- **Some endpoints require a token and say so with `401`.** Do not retry a `401`; report that the
  credential is missing.
- **Never invent or replay a credential.** If a call needs one, say so. A `401`/`403` is the answer
  to report, not a puzzle to solve by trying variants.
- **A proxy or TLS inspection** on a corporate machine shows up as a certificate error. Report it;
  never pass `-SkipCertificateCheck` or `curl.exe -k` to get past it.

## Local APIs, which is where the traps are different

- **`localhost` and `127.0.0.1` are not always interchangeable**: a service bound to `::1` only, or
  bound with a Host-header check, answers differently to each. If one fails, try the other before
  concluding the service is down.
- **A health endpoint is not a readiness endpoint.** Prefer the endpoint that exercises the thing
  you depend on.
- **A `2xx` is not "the effect happened".** Verify by reading back what should have changed: the
  file, the record, the list. This applies most to `POST`.

## Report the call

Give the method, the URL and what came back, including the status. "Got the version from the PyPI
JSON for `requests`" is checkable; "the version is 2.x" is not. And if you could not call — plan
mode, a refusal, a timeout — say exactly that rather than answering from memory as if you had
fetched it.
