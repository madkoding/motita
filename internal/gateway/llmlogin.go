package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/onboard"
)

// SIGNING IN TO A PROVIDER FROM THE BROWSER, instead of pasting a key.
//
// The same logins the setup wizard runs (see onboard/directauth.go), driven over HTTP: the gateway
// starts the flow, the page shows the link and the code, and the gateway waits for the provider in
// the background and stores the login where the wizard stores it (config.AuthDir). Nothing secret
// travels to the page: it only learns that the login is pending, connected or failed.
//
//   - copilot is a device flow: open a link, type a code.
//   - codex is a redirect to localhost:1455 on the gateway's machine. A browser on that machine
//     completes it on its own; any other pastes the address the browser ended on.
//   - gemini uses gcloud's Application Default Credentials on the gateway's machine, at once.

// loginFlow is a started login: what the page shows, and how to wait for its end.
type loginFlow struct {
	Method string // device | code | instant
	URL    string
	Code   string
	// Wait blocks until the provider answers. pasted carries what the user pasted (code flows).
	Wait func(ctx context.Context, pasted <-chan string) (oauth.Credential, error)
}

// loginTTL bounds a login left pending: the provider's code expires long before.
const loginTTL = 15 * time.Minute

// llmLoginStarters start the login of each provider that has one. A variable so a test drives the
// HTTP side with a fake flow.
var llmLoginStarters = map[string]func(ctx context.Context) (loginFlow, error){
	"copilot": copilotLogin,
	"codex":   codexLogin,
	"gemini":  geminiLogin,
}

// startCodexLoopback is oauth.StartLoopback, as a variable so a test needs no port 1455.
var startCodexLoopback = oauth.StartLoopback

func copilotLogin(ctx context.Context) (loginFlow, error) {
	cfg := oauth.CopilotConfig{ClientID: oauth.CopilotClientID}
	dc, err := oauth.CopilotRequestDeviceCode(ctx, nil, cfg)
	if err != nil {
		return loginFlow{}, err
	}
	return loginFlow{Method: "device", URL: dc.VerificationURL, Code: dc.UserCode,
		Wait: func(ctx context.Context, _ <-chan string) (oauth.Credential, error) {
			gh, err := oauth.PollDevice(ctx, dc.Interval, oauth.PollSleep, func() (string, error) {
				return oauth.CopilotPollAccessToken(ctx, nil, cfg, dc)
			})
			if err != nil {
				return oauth.Credential{}, err
			}
			ct, err := oauth.CopilotExchangeToken(ctx, nil, gh)
			if err != nil || ct.Token == "" {
				return oauth.Credential{}, errors.New("GitHub gave no Copilot access: is the Copilot subscription active?")
			}
			return oauth.CopilotAuthResult{GitHubToken: gh, CopilotToken: ct}.Credential(), nil
		}}, nil
}

func codexLogin(context.Context) (loginFlow, error) {
	pkce, err := oauth.NewPKCE()
	if err != nil {
		return loginFlow{}, err
	}
	// Best effort: a port already taken leaves the paste, which always works.
	lb, _ := startCodexLoopback(oauth.CodexListenAddr, "localhost", "/auth/callback")
	return loginFlow{Method: "code", URL: oauth.CodexAuthorizeURL(pkce, oauth.CodexRedirectURI),
		Wait: func(ctx context.Context, pasted <-chan string) (oauth.Credential, error) {
			defer lb.Close()
			code, _, err := lb.Wait(ctx, pkce.State, pasted)
			if err != nil {
				return oauth.Credential{}, err
			}
			return oauth.CodexExchangeCode(ctx, nil, code, oauth.CodexRedirectURI, pkce)
		}}, nil
}

// loginRefusal is a login that cannot start, with a code the page turns into a sentence in the
// user's language and the English text for any other client. It never carries a path or a command
// output: those are for the gateway's log, not for the person at the page.
type loginRefusal struct{ code, msg string }

func (e *loginRefusal) Error() string { return e.msg }

func geminiLogin(ctx context.Context) (loginFlow, error) {
	cred, err := oauth.GeminiFromADC(oauth.ADCPath(config.HomeDir()), "")
	if errors.Is(err, fs.ErrNotExist) {
		return loginFlow{}, &loginRefusal{"gemini_no_gcloud",
			"Google sign-in is not set up on this computer: paste an AI Studio key instead"}
	}
	if err != nil {
		return loginFlow{}, &loginRefusal{"gemini_bad_login",
			"The Google login on this computer cannot be used: paste an AI Studio key instead"}
	}
	if cred.ProjectID == "" {
		return loginFlow{}, &loginRefusal{"gemini_no_project",
			"The Google login has no Google Cloud project: paste an AI Studio key instead"}
	}
	cred, err = oauth.GeminiRefresh(ctx, nil, cred)
	if err != nil {
		return loginFlow{}, &loginRefusal{"gemini_expired",
			"The Google login on this computer has expired: paste an AI Studio key instead"}
	}
	return loginFlow{Method: "instant",
		Wait: func(context.Context, <-chan string) (oauth.Credential, error) { return cred, nil }}, nil
}

// llmLogin is one login in progress, as the gateway tracks it.
type llmLogin struct {
	id, provider string
	flow         loginFlow
	paste        chan string
	cancel       context.CancelFunc

	mu     sync.Mutex
	status string // pending | connected | failed
	err    string
}

// loginView is what the page is told about a login.
type loginView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Method   string `json:"method"`
	URL      string `json:"url,omitempty"`
	Code     string `json:"code,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

func (l *llmLogin) view() loginView {
	l.mu.Lock()
	defer l.mu.Unlock()
	return loginView{ID: l.id, Provider: l.provider, Method: l.flow.Method, URL: l.flow.URL, Code: l.flow.Code,
		Status: l.status, Error: l.err}
}

func (l *llmLogin) finish(status, msg string) {
	l.mu.Lock()
	l.status, l.err = status, msg
	l.mu.Unlock()
}

// llmLoginRoutes registers the login endpoints.
func (s *Server) llmLoginRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /v1/llm/providers/{id}/login", plain(s.handleLLMLogin))
	mux.Handle("GET /v1/llm/logins/{flow}", plain(s.handleLLMLoginStatus))
	mux.Handle("POST /v1/llm/logins/{flow}/paste", plain(s.handleLLMLoginPaste))
	mux.Handle("DELETE /v1/llm/logins/{flow}", plain(s.handleLLMLoginCancel))
}

// handleLLMLogin starts a provider's login and answers what the page shows.
func (s *Server) handleLLMLogin(w http.ResponseWriter, r *http.Request) {
	p, ok := lookupProvider(w, r)
	if !ok {
		return
	}
	start, ok := llmLoginStarters[p.ID]
	if !ok {
		writeError(w, http.StatusBadRequest, p.Name+" has no sign-in: connect it with a key")
		return
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, loginTTL)
	flow, err := start(ctx)
	if err != nil {
		cancel()
		var refusal *loginRefusal
		if errors.As(err, &refusal) {
			writeRefusal(w, http.StatusBadGateway, refusal.code, refusal.msg)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	l := &llmLogin{id: hex.EncodeToString(b), provider: p.ID, flow: flow, paste: make(chan string, 1),
		cancel: cancel, status: "pending"}
	s.llmLoginMu.Lock()
	if s.llmLogins == nil {
		s.llmLogins = map[string]*llmLogin{}
	}
	// A login that ended was read by the page that started it; a new one is when to let it go.
	for id, old := range s.llmLogins {
		if old.view().Status != "pending" {
			delete(s.llmLogins, id)
		}
	}
	s.llmLogins[l.id] = l
	s.llmLoginMu.Unlock()
	done := make(chan struct{})
	go s.waitLogin(ctx, l, p, done)
	if flow.Method == "instant" {
		<-done
	}
	writeJSON(w, http.StatusOK, l.view())
}

// waitLogin waits for the provider, stores the login and hands it to the open sessions.
func (s *Server) waitLogin(ctx context.Context, l *llmLogin, p onboard.Provider, done chan<- struct{}) {
	defer close(done)
	defer l.cancel()
	cred, err := l.flow.Wait(ctx, l.paste)
	if err == nil {
		cred.Provider = p.ID
		err = saveLogin(config.AuthDir(), cred)
	}
	if err != nil {
		l.finish("failed", err.Error())
		return
	}
	// The sessions first: once the page reads "connected", they already run on the login.
	s.refreshSessions(p.ID)
	l.finish("connected", "")
}

// saveLogin is oauth.SaveCredential, as a variable so a test can make it fail.
var saveLogin = oauth.SaveCredential

// loginOf resolves the {flow} of the path, answering 404 for one this gateway does not hold.
func (s *Server) loginOf(w http.ResponseWriter, r *http.Request) (*llmLogin, bool) {
	s.llmLoginMu.Lock()
	l, ok := s.llmLogins[r.PathValue("flow")]
	s.llmLoginMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no such login: it ended or was never started")
	}
	return l, ok
}

func (s *Server) handleLLMLoginStatus(w http.ResponseWriter, r *http.Request) {
	if l, ok := s.loginOf(w, r); ok {
		writeJSON(w, http.StatusOK, l.view())
	}
}

// handleLLMLoginPaste hands a login the address (or the code) the browser ended on.
func (s *Server) handleLLMLoginPaste(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	l, ok := s.loginOf(w, r)
	if !ok {
		return
	}
	if l.flow.Method != "code" || strings.TrimSpace(body.Code) == "" {
		writeError(w, http.StatusBadRequest, "this login takes no pasted code")
		return
	}
	select {
	case l.paste <- body.Code:
	default:
		// One is already waiting to be read: the login is finishing.
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleLLMLoginCancel stops a login and forgets it.
func (s *Server) handleLLMLoginCancel(w http.ResponseWriter, r *http.Request) {
	l, ok := s.loginOf(w, r)
	if !ok {
		return
	}
	l.cancel()
	s.llmLoginMu.Lock()
	delete(s.llmLogins, l.id)
	s.llmLoginMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
