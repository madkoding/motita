package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
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
	case "ollama":
		return ollamaBaseURL
	case "anthropic":
		return anthropicBaseURL
	case "gemini":
		return geminiBaseURL
	case "copilot":
		return copilotBaseURL
	case "qwen":
		return qwenKeyBaseURL
	}
	return ""
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
	}
	return nil
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
	// Another process (the gateway, a second terminal) may have renewed it already:
	// a rotated refresh token can be spent only once, so the newer file wins.
	if s.dir != "" {
		if disk, err := oauth.LoadCredential(s.dir, s.provider); err == nil &&
			disk.AccessToken != s.cred.AccessToken && !disk.NeedsRefresh(s.now()) {
			s.cred = disk
			return s.cred, nil
		}
	}
	fresh, err := s.refresh(ctx, s.hc, s.cred)
	if err != nil {
		return oauth.Credential{}, fatalError{fmt.Errorf("the %s login could not be renewed: %w (log in again with `motita -init`)", s.provider, err)}
	}
	s.cred = fresh
	if s.dir != "" {
		if err := oauth.SaveCredential(s.dir, fresh); err != nil {
			return oauth.Credential{}, fmt.Errorf("the %s login was renewed but could not be saved: %w", s.provider, err)
		}
	}
	return s.cred, nil
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
			if key != "" {
				t.headers["Authorization"] = "Bearer " + key
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
		if cred.BaseURL != "" {
			t.base = strings.TrimRight(cred.BaseURL, "/")
		}
		for k, v := range oauth.CopilotRequiredHeaders(cred.AccessToken) {
			t.headers[k] = v
		}
	case "codex":
		t.base = oauth.CodexBackendURL
		t.chatgpt = true
		t.headers["Authorization"] = "Bearer " + cred.AccessToken
		t.headers["chatgpt-account-id"] = cred.AccountID
		t.headers["OpenAI-Beta"] = "responses=experimental"
		t.headers["originator"] = "codex_cli_rs"
	case "qwen":
		// A qwen.ai token is valid on the host its login named, never on the
		// DashScope endpoint a key would use, so base_url does not apply here.
		t.base = oauth.QwenDefaultBaseURL
		if cred.BaseURL != "" {
			t.base = strings.TrimRight(cred.BaseURL, "/")
		}
		t.headers["Authorization"] = "Bearer " + cred.AccessToken
	case "gemini":
		t.headers["Authorization"] = "Bearer " + cred.AccessToken
		if cred.ProjectID != "" {
			t.headers["x-goog-user-project"] = cred.ProjectID
		}
	}
	return t, nil
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
	relearned := false
	for attempt := 0; ; attempt++ {
		t, err := c.target(ctx, attempt > 0)
		if err != nil {
			return nil, t, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+path, bytes.NewReader(data))
		if err != nil {
			return nil, t, fmt.Errorf("invalid request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}
		for k, v := range t.headers {
			req.Header.Set(k, v)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, t, err // network error: retryable
		}
		if resp.StatusCode == http.StatusOK {
			return resp, t, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && t.login && attempt == 0 {
			continue
		}
		// Asked for more output than the model allows: the refusal names the limit.
		// Ask once more within it, and keep it for the next request.
		if resp.StatusCode == http.StatusBadRequest && !relearned {
			if asked := requestedOutputLimit(data); asked > 0 {
				if limit := limitFromRefusal(asked, string(b)); limit > 0 {
					relearned = true
					c.outputCap.Store(int64(limit))
					c.log.Warn("the model allows less output than max_tokens asks for; using its limit",
						"asked", asked, "limit", limit)
					data = c.clampOutputLimit(data)
					continue
				}
			}
		}
		return nil, t, &HTTPError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
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
		return c.listJSON(ctx, "/v1/models", func(b []byte) []string {
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
		})
	case "gemini":
		return c.listJSON(ctx, "/v1beta/models?pageSize=1000", func(b []byte) []string {
			var p struct {
				Models []struct {
					Name    string   `json:"name"`
					Methods []string `json:"supportedGenerationMethods"`
				} `json:"models"`
			}
			_ = json.Unmarshal(b, &p)
			out := make([]string, 0, len(p.Models))
			for _, m := range p.Models {
				for _, meth := range m.Methods {
					if meth == "generateContent" {
						out = append(out, strings.TrimPrefix(m.Name, "models/"))
						break
					}
				}
			}
			return out
		})
	}
	t, err := c.target(ctx, false)
	if err != nil {
		return nil, err
	}
	if t.chatgpt {
		// The ChatGPT backend publishes no catalogue to a third party.
		return nil, errors.New("a ChatGPT login does not publish a model list; type the model id")
	}
	return fetchModelList(ctx, t.base+"/models", t.headers)
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
		return nil, err
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
