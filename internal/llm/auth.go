package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

// Literals used more than once in this file.
const (
	bearerPrefix = "Bearer "
)

// Default endpoints, one per provider. The configuration's base_url wins over
// them, except where a login names its own host (Copilot's plan endpoint, the
// Qwen resource host, the ChatGPT backend): a token is only valid there.
const (
	openAIBaseURL    = "https://api.openai.com/v1"
	ollamaBaseURL    = "https://ollama.com/v1"
	anthropicBaseURL = "https://api.anthropic.com"
	geminiBaseURL    = "https://generativelanguage.googleapis.com"
	copilotBaseURL   = "https://api.githubcopilot.com"
	// qwenKeyBaseURL is DashScope's international OpenAI-compatible endpoint, the one
	// a key from the Model Studio console works with outside mainland China.
	qwenKeyBaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
)

// DefaultBaseURL is the endpoint a provider is reached at when nothing else names one.
func DefaultBaseURL(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "codex":
		return openAIBaseURL
	case "ollama", "ollama-cloud":
		return ollamaBaseURL
	case "anthropic":
		return anthropicBaseURL
	case "gemini":
		return geminiBaseURL
	case "copilot":
		return copilotBaseURL
	case "qwen":
		return qwenKeyBaseURL
	default:
		return ""
	}
}

// target is where one request goes and what proves who sends it.
type target struct {
	base    string
	headers map[string]string
	// login is true when the credential is a stored login rather than a key: a 401
	// then means "renew and try once more", not "the key is wrong".
	login bool
	// chatgpt is true for a Codex request billed to a ChatGPT plan, which goes to
	// the ChatGPT backend and has to be streamed.
	chatgpt bool
}

// loginState is a stored login being used by this client: the credential, how
// to renew it, and where to write the renewed one.
type loginState struct {
	mu       sync.Mutex
	provider string
	dir      string // "" for a credential that lives only in memory
	cred     oauth.Credential
	refresh  func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error)
	hc       oauth.HTTPClient
	now      func() time.Time
}

// refresherFor names the renewal of each provider's login.
func refresherFor(provider string) func(context.Context, oauth.HTTPClient, oauth.Credential) (oauth.Credential, error) {
	switch provider {
	case "copilot":
		return oauth.CopilotRefreshCredential
	case "codex":
		return oauth.CodexRefresh
	case "qwen":
		return oauth.QwenRefresh
	case "gemini":
		return oauth.GeminiRefresh
	default:
		return nil
	}
}

// newLoginState prepares the login a client uses, or nil when it uses a key.
//
// The rules, in order:
//   - copilot with a key: the key IS a GitHub token, exchanged for session tokens in
//     memory (the Copilot API never accepts the GitHub token itself);
//   - any other key: the key is used as is, and no login is read;
//   - no key: the stored login of the provider, when it supports one.
func newLoginState(cfg config.LLM, hc *http.Client) (*loginState, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	refresh := refresherFor(provider)
	if refresh == nil {
		return nil, nil
	}
	st := &loginState{provider: provider, refresh: refresh, hc: hc, now: time.Now}
	if cfg.APIKey != "" {
		if provider != "copilot" {
			return nil, nil
		}
		st.cred = oauth.Credential{Provider: provider, RefreshToken: cfg.APIKey}
		return st, nil
	}
	dir := config.AuthDir()
	cred, err := oauth.LoadCredential(dir, provider)
	if errors.Is(err, oauth.ErrNoCredential) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.dir, st.cred = dir, cred
	return st, nil
}

// credential returns a usable credential, renewing it first when it is about to
// expire (or when force is set, after the server refused it).
func (s *loginState) credential(ctx context.Context, force bool) (oauth.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && !s.cred.NeedsRefresh(s.now()) {
		return s.cred, nil
	}
	if s.dir != "" {
		// Every client of this login in this process renews it one at a time: the
		// next one then finds the renewed file below and does not spend the
		// rotated refresh token a second time.
		mu, _ := loginLocks.LoadOrStore(oauth.CredentialPath(s.dir, s.provider), new(sync.Mutex))
		mu.(*sync.Mutex).Lock()
		defer mu.(*sync.Mutex).Unlock()
	}
	// Another client (the gateway, a second terminal) may have renewed it already:
	// a rotated refresh token can be spent only once, so the newer file wins.
	if s.adoptStored() {
		return s.cred, nil
	}
	fresh, err := s.refresh(ctx, s.hc, s.cred)
	if err != nil {
		// Another process may have spent the refresh token first and saved its renewal.
		if s.adoptStored() {
			return s.cred, nil
		}
		return oauth.Credential{}, fatalError{fmt.Errorf(
			"the %s login could not be renewed: %w (log in again with `motita config`)", s.provider, err)}
	}
	s.cred = fresh
	if s.dir != "" {
		if err := oauth.SaveCredential(s.dir, fresh); err != nil {
			return oauth.Credential{}, fmt.Errorf("the %s login was renewed but could not be saved: %w", s.provider, err)
		}
	}
	return s.cred, nil
}

// loginLocks holds one *sync.Mutex per stored login file, shared by every client of the process.
var loginLocks sync.Map

// adoptStored takes the stored login when another client renewed it: a different token that is
// still valid. It reports whether it did.
func (s *loginState) adoptStored() bool {
	if s.dir == "" {
		return false
	}
	disk, err := oauth.LoadCredential(s.dir, s.provider)
	if err != nil || disk.AccessToken == s.cred.AccessToken || disk.NeedsRefresh(s.now()) {
		return false
	}
	s.cred = disk
	return true
}

// target resolves where the next request goes and how it is authenticated.
func (c *Client) target(ctx context.Context, force bool) (target, error) {
	provider := strings.ToLower(c.cfg.Provider)
	base := c.baseURL(DefaultBaseURL(provider))
	key := c.cfg.APIKey

	if c.login == nil {
		t := target{base: base, headers: map[string]string{}}
		switch provider {
		case "anthropic":
			t.headers["x-api-key"] = key
			t.headers["anthropic-version"] = "2023-06-01"
		case "gemini":
			// In a header, never in the URL: a URL is what net/http prints in every
			// transport error, and a key in it ended up in the logs.
			t.headers["x-goog-api-key"] = key
		default:
			if key != "" && !config.IsSelfHostedOllama(c.cfg) {
				t.headers["Authorization"] = bearerPrefix + key
			}
		}
		return t, nil
	}

	cred, err := c.login.credential(ctx, force)
	if err != nil {
		return target{}, err
	}
	t := target{base: base, login: true, headers: map[string]string{}}
	switch provider {
	case "copilot":
		t.useCopilotLogin(cred)
	case "codex":
		t.useCodexLogin(cred)
	case "qwen":
		// A qwen.ai token is valid on the host its login named, never on the
		// DashScope endpoint a key would use, so base_url does not apply here.
		// A host stored by an older version as "http://" is upgraded to https.
		t.base = oauth.QwenBaseURL(cred.BaseURL)
		t.headers["Authorization"] = bearerPrefix + cred.AccessToken
	case "gemini":
		t.useGeminiLogin(cred)
	default:
		// newLoginState gives a login only to the providers above.
	}
	return t, nil
}

// useCopilotLogin points t at the host the Copilot session token names, with the headers the
// Copilot API requires.
func (t *target) useCopilotLogin(cred oauth.Credential) {
	if cred.BaseURL != "" {
		t.base = strings.TrimRight(cred.BaseURL, "/")
	}
	for k, v := range oauth.CopilotRequiredHeaders(cred.AccessToken) {
		t.headers[k] = v
	}
}

// useCodexLogin points t at the ChatGPT backend, which a Codex request billed to a ChatGPT plan
// goes to.
func (t *target) useCodexLogin(cred oauth.Credential) {
	t.base = oauth.CodexBackendURL
	t.chatgpt = true
	t.headers["Authorization"] = bearerPrefix + cred.AccessToken
	t.headers["chatgpt-account-id"] = cred.AccountID
	t.headers["OpenAI-Beta"] = "responses=experimental"
	t.headers["originator"] = "codex_cli_rs"
}

// useGeminiLogin authenticates t with a Google login, billed to its project when it names one.
func (t *target) useGeminiLogin(cred oauth.Credential) {
	t.headers["Authorization"] = bearerPrefix + cred.AccessToken
	if cred.ProjectID != "" {
		t.headers["x-goog-user-project"] = cred.ProjectID
	}
}

// send makes one request to path under the provider's base URL. A stored login
// the server refuses (401) is renewed and the request sent once more, because a
// token can be revoked or expire early; a key that is refused stays refused.
//
// The response is returned open on 200 and closed with an *HTTPError otherwise.
func (c *Client) send(ctx context.Context, path string, body any, stream bool) (*http.Response, target, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, target{}, fmt.Errorf("could not serialise the request: %w", err)
	}
	data = c.clampOutputLimit(data)
	// renew forces a renewal on the pass right after a 401, and only then: asking again within
	// the output limit is no reason to spend a rotating refresh token.
	relearned, renewed, renew, unschemed := false, false, false, false
	for {
		t, err := c.target(ctx, renew)
		renew = false
		if err != nil {
			return nil, t, err
		}
		req, err := newPostRequest(ctx, t, path, data, stream)
		if err != nil {
			return nil, t, err
		}
		resp, err := c.do(req, stream)
		if err != nil {
			return nil, t, Explain(err) // network error: retryable
		}
		if resp.StatusCode == http.StatusOK {
			if r, ok := limitsFrom(resp.Header, time.Now()); ok {
				totals.limits(c.source(), r)
			}
			return resp, t, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && t.login && !renewed {
			renewed, renew = true, true
			continue
		}
		// Asked for more output than the model allows: the refusal names the limit.
		// Ask once more within it, and keep it for the next request.
		if resp.StatusCode == http.StatusBadRequest && !relearned && c.relearnOutputLimit(data, b) {
			relearned = true
			data = c.clampOutputLimit(data)
			continue
		}
		if !unschemed && c.refusedSchema(&data, resp.StatusCode) {
			unschemed = true
			continue
		}
		return nil, t, &HTTPError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
}

// do sends req. A plain request is bounded as a whole by llm.timeout. A stream is not: its answer
// can take far longer than that while it keeps coming, so llm.timeout bounds the wait for the
// headers and then each gap between two reads of the body. Closing the body stops the watch.
func (c *Client) do(req *http.Request, stream bool) (*http.Response, error) {
	if !stream {
		return c.http.Do(req)
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	// The cause reads as a timeout (net.Error), so the stream is retried like any other.
	watch := time.AfterFunc(c.cfg.Timeout, func() { cancel(context.DeadlineExceeded) })
	hc := *c.http
	hc.Timeout = 0
	resp, err := hc.Do(req.WithContext(ctx))
	if err != nil {
		watch.Stop()
		cancel(nil)
		return nil, err
	}
	resp.Body = &watchedBody{ReadCloser: resp.Body, watch: watch, timeout: c.cfg.Timeout, cancel: cancel}
	return resp, nil
}

// watchedBody is a stream's body that restarts its watch on every read that brings data.
type watchedBody struct {
	io.ReadCloser
	watch   *time.Timer
	timeout time.Duration
	cancel  context.CancelCauseFunc
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.watch.Reset(b.timeout)
	}
	return n, err
}

func (b *watchedBody) Close() error {
	b.watch.Stop()
	b.cancel(nil)
	return b.ReadCloser.Close()
}

// newPostRequest builds the POST of data to path under t's base URL, with t's headers.
func newPostRequest(ctx context.Context, t target, path string, data []byte, stream bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// relearnOutputLimit keeps the output limit a refusal of data names, and reports whether it found
// one: the request can then be asked once more within it.
func (c *Client) relearnOutputLimit(data, refusal []byte) bool {
	asked, limit := requestedOutputLimit(data), 0
	if asked > 0 {
		limit = limitFromRefusal(asked, string(refusal))
	}
	if limit <= 0 {
		return false
	}
	c.outputCap.Store(int64(limit))
	c.log.Warn("the model allows less output than max_tokens asks for; using its limit",
		"asked", asked, "limit", limit)
	return true
}

// post is send for a non-streaming request: it returns the whole body.
func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	resp, _, err := c.send(ctx, path, body, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// ListModels lists the models of the configured provider with its own
// credentials, so a login (Copilot, Qwen) can list what it is entitled to.
// claude-code is not listed here: its catalogue is the claude CLI's own
// (ClaudeCodeModels).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	provider := strings.ToLower(c.cfg.Provider)
	switch provider {
	case "anthropic":
		return c.listJSON(ctx, "/v1/models", parseAnthropicModels)
	case "gemini":
		return c.listJSON(ctx, "/v1beta/models?pageSize=1000", parseGeminiModels)
	default:
		// Every other provider serves an OpenAI-style /models, read below.
	}
	t, err := c.target(ctx, false)
	if err != nil {
		return nil, err
	}
	if t.chatgpt {
		// The ChatGPT backend publishes no catalogue to a third party.
		return nil, errors.New("a ChatGPT login does not publish a model list; type the model id")
	}
	return fetchModelList(ctx, c.http.Transport, t.base+"/models", t.headers)
}

// parseAnthropicModels reads the model ids of Anthropic's /v1/models.
func parseAnthropicModels(b []byte) []string {
	var p struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(b, &p)
	out := make([]string, 0, len(p.Data))
	for _, m := range p.Data {
		out = append(out, m.ID)
	}
	return out
}

// parseGeminiModels reads the ids of the Gemini models that can generate content.
func parseGeminiModels(b []byte) []string {
	var p struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	_ = json.Unmarshal(b, &p)
	out := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		if slices.Contains(m.Methods, "generateContent") {
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	return out
}

func (c *Client) listJSON(ctx context.Context, path string, parse func([]byte) []string) ([]string, error) {
	t, err := c.target(ctx, false)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, Explain(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	names := parse(b)
	if len(names) == 0 {
		return nil, fmt.Errorf("no models were listed by %s", t.base)
	}
	return names, nil
}
