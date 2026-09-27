# Searching the web

Use when the answer is not on this machine: the current version of something, the shape of an
API, an error message you have not seen, official documentation, a changelog, a paper. Also use
it when you are about to state a fact you are not certain of — a version number, a flag, a URL —
because a plausible guess is worse than a fetched line.

## First: whether you can reach the network AT ALL, because it differs by mode

| Mode | Can you fetch? | What happens |
| --- | --- | --- |
| **Task** (writes) | **Yes, with one confirmation** | `curl` is classified `external-effect`: the user is asked once per line. Approved, it runs normally. |
| **Plan** (read-only) | **No** | `curl`, `wget`, `python3` and `nc` are refused BY NAME as writers. `w3m` and `lynx` are refused as unclassified. No amount of phrasing gets a fetch through. |

So in **plan mode there is no web access**, and the honest answer is to say so and reason from
what you have. Do not present a remembered version number as a fetched one: in plan mode you
cannot have fetched anything. Only name lookups work there — `dig`, `host` and `getent` are on the
reader list.

Because a fetch costs the user one confirmation, **fetch ONCE per question and get everything in
that one call**. A line that goes out five times is five questions on the screen.

## The one shape that works in both modes, minus the shell

Read-only mode runs the program **directly**, with no shell: a pipe, a `;`, a `&&` or a `$()` is
impossible there, not merely refused. Task mode runs the whole line through a shell, so a pipeline
works — but it is classified as one line, and the worst segment decides.

That gives one rule that pays off in both: **prefer a single program with all its arguments over
a pipeline.** The commands below are written that way on purpose.

## What to fetch, per kind of question

These are the endpoints that were measured working from this machine. Each returns JSON, so one
field can be read without pulling a page in.

| Question | Command |
| --- | --- |
| The official summary of a topic | `curl -s -f "https://en.wikipedia.org/api/rest_v1/page/summary/<Title>"` |
| What pages exist about it | `curl -s -f "https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch=<words>&format=json&srlimit=5"` |
| A Python package's version and metadata | `curl -s -f "https://pypi.org/pypi/<package>/json"` |
| An npm package's latest version | `curl -s -f "https://registry.npmjs.org/<package>/latest"` |
| A repository's description, stars, last push | `curl -s -f "https://api.github.com/repos/<owner>/<repo>"` |
| Repositories matching a query | `curl -s -f "https://api.github.com/search/repositories?q=<words>&per_page=5"` |
| What people have written about a topic | `curl -s -f "https://hn.algolia.com/api/v1/search?query=<words>&hitsPerPage=5"` |
| Questions already answered | `curl -s -f "https://api.stackexchange.com/2.3/search?order=desc&sort=relevance&intitle=<words>&site=stackoverflow"` |
| An academic paper | `curl -s -f "https://export.arxiv.org/api/query?search_query=all:<words>&max_results=5"` |
| A page as readable text | `curl -s -f "https://r.jina.ai/https://<the-page>"` |

**There is no general web search that works reliably from here.** DuckDuckGo's HTML endpoint
answers `202` with a challenge page instead of results — measured six times in a row, zero results
every time — and its Instant Answer API returns an empty body for a normal query. Mojeek answers
`403`, Startpage redirects away. So do not build a procedure on a general search engine: **go to
the API of the site that owns the answer.** Wikipedia for a definition, the package registry for a
version, the code host for source and issues, Stack Exchange for a problem somebody already hit.

## One call, several URLs, no shell needed

`curl` takes more than one URL, and `-o -` writes each response to stdout in order. That is how to
fetch two things in one line — and so in one confirmation — without a `;`, a `&&` or a loop:

```sh
curl -s -f -o - "https://pypi.org/pypi/requests/json" "https://registry.npmjs.org/preact/latest"
```

The responses are concatenated with nothing between them. Two JSON objects in one stream are not
one JSON document, so a tool that requires a single document needs them written to separate files
instead: two `-o` targets, and no shell metacharacter either.

## Getting at a field without a pipeline

Task mode has a shell, so `curl ... | grep -o ...` works. **In plan mode it does not exist**, and a
single program that can read a field is the shape that survives both. When a pipeline really is the
right tool, remember what the classifier does with it: `curl | grep` and `curl | cut` stay *ask*
even under strict, while `curl | python3` and `curl | w3m` become a hard *deny* under
`agent.policy.strict`. Readers pipe safely; interpreters do not.

Practical consequences:

- **Write the response to a file, then read the field with a reader.** Two calls, and both are
  ordinary work:
  ```sh
  curl -s -f -o /tmp/answer.json "https://en.wikipedia.org/api/rest_v1/page/summary/Zephyr_(operating_system)"
  grep -o '"extract":"[^"]\{0,80\}' /tmp/answer.json
  ```
- **`grep -o` is the poor man's JSON reader** and it is on the reader list. It is enough for one
  flat field. It is not enough for nested data — and note that `jq` is on the reader list but is
  **not installed** on every machine, so reach for `grep` first and check `jq` only if the shape
  demands it.
- **`w3m -dump` turns a page into text** and it is installed here, but it is *unclassified*, so it
  costs a confirmation in task mode and is refused in plan mode. `r.jina.ai` above does the same job
  through a plain `curl`, which is the cheaper route when the page must be read.
- **`sed`, `awk` and `python3` are writers by definition**, refused in plan mode and *deny* under
  strict. Never reach for them to slice JSON.

## Keep the output SMALL, on the server's side

The tool output is capped at 64 KiB and a file read at 1 MiB, so a fetched page is silently
truncated if it is big — and the truncation lands wherever the cap falls, not at the end of a
record. The two places that cost most:

- **A package's full metadata is large**: `pypi.org/pypi/<package>/json` measured 192 KB for one
  ordinary package. Save it to a file and grep the field; never let it land whole in the context.
- **Stack Exchange serves gzip.** `curl --compressed` took the same response from 20 118 bytes to
  5 009. Add `--compressed` and the same call costs a quarter of the bytes.

Set a timeout on anything that leaves the machine. A hanging fetch is a hung task:

```sh
curl -s -f -m 20 -o /tmp/answer.json "https://api.github.com/repos/zephyrproject-rtos/zephyr"
```

## Always `-f`, and always check

`curl` exits `0` on an HTTP error and hands you the error body as if it were the answer.
**Measured**: a `404` exits `22` with `-f` and `0` without it. So a line without `-f` turns a typo
in a URL into "the answer says `{"status":404,...}`", which a tired reader takes for data.

- `-f` makes a non-2xx exit non-zero, so the failure is visible.
- A GitHub **code** search (`/search/code`) answers `401` without a token: only the repository
  search works unauthenticated. Do not retry it — say the token is missing.
- GitHub's unauthenticated search limit is **10 requests per minute** (`x-ratelimit-limit: 10`).
  A loop over a search endpoint exhausts it and then answers `403` for everything.
- If a fetch fails, **quote what came back** — the status, the body — rather than summarising it as
  "could not fetch". The status is the diagnosis.

## Say what you fetched, and when you did not

- Give the URL you read, and the field you took the answer from. "Per the PyPI JSON for `requests`"
  is checkable; "the latest version is 2.x" is not.
- **A fetched value beats your memory, always.** Version numbers, flags, endpoint shapes and
  default values change; the page is current and the model is not.
- If you could not fetch — plan mode, a refusal, a timeout — say exactly that, and mark the answer
  as recalled rather than fetched. An honest gap is useful; an invented version number is a defect
  somebody will act on.
- Do not re-run a line the user declined. Ask a narrower question instead, or say the fetch is
  needed and why.
