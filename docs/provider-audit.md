# Audit: connecting to LLM providers (OAuth and API key)

Date: 2026-10-01 · Scope: `internal/llm`, `internal/oauth`, `internal/onboard`,
`internal/config`, and switching providers in `internal/tui` and `internal/gateway`.

Required providers: **OpenAI, Ollama, Claude, Gemini, Qwen, Codex and GitHub Copilot**.

## Summary

Before this audit **no OAuth login worked end to end**, and several API key paths did not
either. Qwen and local Ollama did not exist. Everything found is fixed and covered by tests
on this branch.

| Provider | API key: before → now | Account login: before → now |
|---|---|---|
| OpenAI | ⚠️ reasoning models (o-series, gpt-5) rejected `max_tokens`/`temperature` → ✅ | n/a |
| Codex | ❌ `*-codex` models were sent to `/chat/completions` (404) → ✅ `/responses` | ❌ did not exist → ✅ "Sign in with ChatGPT" (PKCE) |
| GitHub Copilot | ❌ the GitHub token was sent as is, with no exchange and no headers → ✅ | ❌ the login aborted on `slow_down` and the token never worked → ✅ |
| Ollama | ✅ Cloud · ❌ local required a key → ✅ local with no key | n/a |
| Claude (Anthropic) | ❌ tools were declared wrongly (400) → ✅ | ❌ OAuth impersonating Claude Code: against the terms, and it did not work either → removed; the subscription goes through `claude-code` (the official CLI) ✅ |
| Gemini | ❌ tools declared wrongly; the key travelled in the URL → ✅ | ❌ device flow not accepted by Google with Gemini CLI's embedded secret → ✅ Google login with your own client, or gcloud ADC |
| Qwen | ❌ did not exist → ✅ DashScope | ❌ did not exist → ✅ qwen.ai device code |

## Findings

### Critical (the provider did not work)

1. **Copilot: the GitHub token was never exchanged.** `llm.go` said the exchange lived in
   `internal/llm/copilot.go`, but that file did not exist. The `gho_…` token was sent as a
   Bearer to `api.githubcopilot.com` without `Editor-Version` or `Copilot-Integration-Id`,
   and the API rejects it. **Fix:** `llm/auth.go` exchanges the token, renews it before it
   expires (~30 min), uses the plan's endpoint (individual/business/enterprise) and adds the
   headers.
2. **Copilot: `slow_down` aborted the login.** `CopilotFullFlow` raised the interval and
   then fell into `return err`. The existing test asserted that faulty behaviour.
   **Fix:** `oauth.PollDevice` keeps polling, more slowly (RFC 8628 §3.5).
3. **PKCE computed wrongly.** The *challenge* was the hash of the random bytes rather than
   of the encoded *verifier* that is sent (RFC 7636 §4.2), so no server could validate the
   `code_verifier`: every code exchange failed.
4. **Claude OAuth used the wrong header.** The OAuth token was sent as `x-api-key`, which
   only accepts API keys.
5. **Gemini OAuth:** Google's *device flow* does not grant the Gemini API scopes, the token
   was sent as `?key=` (which only accepts API keys) and the refresh token was not stored.
   It could not have worked.
6. **OAuth tokens expired within hours.** The wizard wrote the access token into a `.env`
   the user loads with `source`, with no refresh token: Gemini died after 1 h, Claude after
   ~8 h. **Fix:** a store at `~/.motita/auth/<provider>.json` (0600, directory 0700, atomic
   writes) with automatic renewal, and one retry after renewing when the server answers 401.
7. **Codex with an API key:** the `gpt-5-codex`/`gpt-5.x-codex` models only exist on
   `/responses`. **Fix:** a Responses API client (`llm/responses.go`) for the `codex`
   provider, with an API key or a ChatGPT login (`chatgpt.com/backend-api/codex`).
8. **Tools on Anthropic and Gemini:** `Parameters` is already a complete JSON Schema and was
   wrapped again as `properties`, declaring three parameters called `type`, `properties` and
   `required`. Both APIs reject it (400), so tool mode was unusable on both. Also:
   - Anthropic: a tool result was preceded by a text block with the same content (the API
     requires `tool_result` first), parallel results went in separate turns, and
     "stringified" arguments were not turned back into an object.
   - Gemini: `functionResponse` carried the call id instead of the function's **name**, and
     a string instead of an object; calls had no id, so the result was sent as loose text;
     the `thoughtSignature` Gemini 3 requires was missing; a tool with no parameters was
     declared with `properties: {}`, which Gemini rejects.
9. **Switching provider in the UI:** `SetLLM` changed the provider but kept the previous
   one's base URL and key: switching from OpenAI to Anthropic sent the next request to
   `api.openai.com` with the OpenAI key. **Fix:** each provider recovers its own URL and key
   (the original ones if it was the configured provider; otherwise its default URL and its
   environment variable). OpenAI and Codex share a key.

### Security and compliance

10. **Gemini key in the URL** (`?key=`): `net/http` prints the URL in every transport error,
    so the key ended up in the logs. It now travels in `x-goog-api-key`.
11. **Gemini CLI's OAuth secret, embedded and obfuscated** on purpose to evade secret
    scanners. Removed: the login uses an OAuth client of the user's own
    (`MOTITA_GEMINI_CLIENT_ID`/`_SECRET`) or `gcloud`'s ADC credentials.
12. **Claude OAuth with Claude Code's `client_id`:** Anthropic only allows Pro/Max
    subscriptions inside its own client. Removed; the legitimate path already existed
    (`claude-code`, which runs the official CLI and never sees the credentials).
13. **`OPENAI_API_KEY` leaked to other providers:** it was used as the Anthropic, Gemini or
    Ollama key when there was no other, sending one provider's key to another. It now only
    applies to `openai` and `codex`; each provider has its own variable (`ANTHROPIC_API_KEY`,
    `GEMINI_API_KEY`, `DASHSCOPE_API_KEY`, `OLLAMA_API_KEY`, `GITHUB_COPILOT_TOKEN`).
14. The local OAuth callback listens on `127.0.0.1` only, validates the `state` and accepts
    the URL pasted by hand (SSH/headless). After a browser login the wizard consumes the
    pending Enter, so the stdin reader does not swallow the next answer.

### Minor

15. Invalid model IDs in the catalogue: Copilot `claude-sonnet-4-20250514` (Copilot uses
    `claude-sonnet-4`), retired Gemini/Anthropic models. Updated.
16. Listing models sent `Authorization: Bearer` to every provider; Anthropic (`x-api-key`)
    and Gemini rejected it. Each provider now lists with its own authentication, logins
    included.
17. The LLM HTTP client ignored `HTTPS_PROXY`; it now uses `ProxyFromEnvironment`.
18. The generated `.env` said `source MOTITA_LLM_API_KEY` instead of the file.

## How each provider authenticates now

| `llm.provider` | API key | Account login | Endpoint |
|---|---|---|---|
| `openai` | `OPENAI_API_KEY` / `MOTITA_LLM_API_KEY` | — | `/chat/completions` (any compatible host) |
| `codex` | `OPENAI_API_KEY` / `MOTITA_LLM_API_KEY` | Sign in with ChatGPT (PKCE, callback on `localhost:1455` or pasted) | `/responses` |
| `copilot` | `GITHUB_COPILOT_TOKEN` (a GitHub OAuth token) | GitHub device code | the plan's `/chat/completions` |
| `ollama` | `OLLAMA_API_KEY` (Cloud only) | — | local `http://localhost:11434/v1` or `https://ollama.com/v1` |
| `anthropic` | `ANTHROPIC_API_KEY` | — | `/v1/messages` |
| `claude-code` | — | `claude auth login` (official CLI) | the `claude` CLI |
| `gemini` | `GEMINI_API_KEY` | Google OAuth with your own client, or gcloud ADC | `generateContent` |
| `qwen` | `DASHSCOPE_API_KEY` | qwen.ai device code | DashScope / the `resource_url` host |

Priority: a configured API key always wins over a stored login.

## Verification

- New tests: `internal/llm/providers_test.go` (every provider and authentication mode
  against simulated servers), `internal/oauth/flows_test.go` (PKCE, store, callback, Codex,
  Qwen, Gemini, Copilot), tests for the wizard and `config`, and for switching provider in
  `internal/tui`.
- `go vet ./...` and `gofmt` clean. `go test ./...` passes except `internal/sandbox`,
  `internal/webui` and `internal/gitx`, which already failed before this change because of
  the environment (no cgroups, web assets not built, the container's global git identity).

## Outstanding (not verifiable from here)

- **No test against the real services:** this environment has no accounts and no access to
  those APIs. A real login on each provider still has to be tried. The most uncertain:
  - the ChatGPT backend for Codex: whether it requires specific `instructions` or rejects a
    field (neither `max_output_tokens` nor `temperature` is sent);
  - Qwen's endpoints (`chat.qwen.ai/api/v1/oauth2/*`) and whether free access through OAuth
    is still available;
  - whether Google accepts the Gemini API scopes with `gcloud`'s default client (the Gemini
    documentation asks for `--client-id-file` with a client of your own).
- **Terms of service:** Copilot (VS Code's client id and the internal `copilot_internal`
  endpoint), Codex (Codex CLI's client id) and Qwen (Qwen Code's client id) reuse the
  identities of official clients, as other third-party tools do. It is worth confirming that
  each provider's current terms allow it.
- The HTTP client of the OAuth flows does not use the embedded CA bundle the LLM client uses
  (relevant on the i386 netbook with old CAs).
- `llm.reasoning` is not yet translated into `thinking` (Anthropic) or `thinkingBudget`
  (Gemini), although `config.go` documents it.
