package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// The git endpoints are what lets a user connect motita to GitHub, GitLab,
// Bitbucket or a Gitea, from the settings or from the new-project dialog, and
// pick a repository of theirs to clone. The logins are the same files the
// credential helper and `motita forge` read, so a connection made here is the
// connection the agent's git uses from a session's worktree.

// gitFlowTTL is how long a login waits for the user's browser before it is
// dropped: the device codes of the hosts live 8 to 15 minutes.
const gitFlowTTL = 15 * time.Minute

// gitFlow is a login in progress. The device or code flow runs in a goroutine of
// its own and the client polls its status.
type gitFlow struct {
	service string
	created time.Time
	cancel  context.CancelFunc
	// paste carries a code the user typed in, for a code flow whose browser is on
	// another machine. Nil for a device flow.
	paste chan string

	status  string // "pending", "connected" or "failed"
	account string
	err     string
}

func (s *Server) gitRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/git/accounts", plain(s.handleGitAccounts))
	mux.Handle("POST /v1/git/connect", plain(s.handleGitConnect))
	mux.Handle("GET /v1/git/flows/{id}", plain(s.handleGitFlow))
	mux.Handle("POST /v1/git/flows/{id}/paste", plain(s.handleGitFlowPaste))
	mux.Handle("DELETE /v1/git/flows/{id}", plain(s.handleGitFlowCancel))
	mux.Handle("DELETE /v1/git/accounts/{id}", plain(s.handleGitDisconnect))
	mux.Handle("GET /v1/git/repos", plain(s.handleGitRepos))
}

// gitStore is the login store, or nil when this gateway has no directory for one.
func (s *Server) gitStore() *gitforge.Store {
	if s.opts.GitAuthDir == "" {
		return nil
	}
	return &gitforge.Store{Dir: s.opts.GitAuthDir, HTTP: s.opts.GitHTTP}
}

// gitStoreOr answers 501 when there is no store, and reports whether to go on.
func (s *Server) gitStoreOr(w http.ResponseWriter) (gitforge.Store, bool) {
	st := s.gitStore()
	if st == nil {
		writeError(w, http.StatusNotImplemented, "this gateway has no directory to keep git logins in")
		return gitforge.Store{}, false
	}
	return *st, true
}

// gitCommandEnv is the environment of a git command the gateway runs itself: the
// process's own plus the credential helper, and git's messages in English, because
// a refusal for credentials is recognised by what git says (see isGitAuthFailure).
func (s *Server) gitCommandEnv() []string {
	env := os.Environ()
	if s.opts.ExePath != "" || s.opts.GitAuthDir != "" {
		env = gitforge.CloneEnv(s.opts.ExePath, s.opts.GitAuthDir)
	}
	return append(env, "LC_ALL=C")
}

// gitAccountView is one host as the settings screen and the project dialog draw it.
type gitAccountView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Host       string   `json:"host"`
	Connected  bool     `json:"connected"`
	Username   string   `json:"username,omitempty"`
	Methods    []string `json:"methods"`
	OAuthReady bool     `json:"oauth_ready"`
	// TokenURL is the page where a token is created by hand.
	TokenURL string `json:"token_url,omitempty"`
	// SetupHint says what turns on the one-click login when it is off.
	SetupHint string `json:"setup_hint,omitempty"`
}

func accountView(a gitforge.Account) gitAccountView {
	v := gitAccountView{
		ID: a.Service.ID, Name: a.Service.Name, Kind: string(a.Service.Kind), Host: a.Service.Host,
		Connected: a.Connected, Username: a.Username, OAuthReady: a.Service.OAuthReady(), TokenURL: a.Service.TokenURL,
	}
	for _, m := range a.Service.Methods() {
		v.Methods = append(v.Methods, string(m))
	}
	if !v.OAuthReady {
		v.SetupHint = "Set " + a.Service.EnvPrefix + "_CLIENT_ID (an OAuth application of yours) to log in with one click, or connect with a token."
	}
	return v
}

func (s *Server) handleGitAccounts(w http.ResponseWriter, _ *http.Request) {
	st, ok := s.gitStoreOr(w)
	if !ok {
		return
	}
	accounts := st.Accounts()
	out := make([]gitAccountView, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, accountView(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// serviceFromRequest resolves the host a request names: by id, or a self-hosted
// one by kind and host.
func serviceFromRequest(st gitforge.Store, id, kind, host string) (gitforge.Service, error) {
	if id != "" {
		if svc, ok := st.ByID(id); ok {
			return svc, nil
		}
		return gitforge.Service{}, errors.New("there is no git host with that id")
	}
	return gitforge.Custom(gitforge.Kind(kind), host, st.Env)
}

func (s *Server) handleGitConnect(w http.ResponseWriter, r *http.Request) {
	st, ok := s.gitStoreOr(w)
	if !ok {
		return
	}
	var body struct {
		Service  string `json:"service"`
		Kind     string `json:"kind"`
		Host     string `json:"host"`
		Method   string `json:"method"`
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	svc, err := serviceFromRequest(st, body.Service, body.Kind, body.Host)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	method := gitforge.Method(body.Method)
	if body.Token != "" {
		method = gitforge.MethodToken
	}
	if method == "" {
		method = svc.Methods()[0]
	}
	switch method {
	case gitforge.MethodToken:
		who, err := st.ConnectToken(r.Context(), svc, body.Token, body.Username)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "connected", "account": who, "service": svc.ID})
	case gitforge.MethodDevice:
		flow, err := st.StartDevice(r.Context(), svc)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		id := s.startGitFlow(svc.ID, nil, func(ctx context.Context, _ <-chan string) (string, error) { return flow.Wait(ctx) })
		writeJSON(w, http.StatusAccepted, map[string]any{
			"flow_id": id, "method": "device", "service": svc.ID,
			"url": flow.URL, "code": flow.Code, "expires_in": flow.ExpiresIn,
		})
	case gitforge.MethodCode:
		flow, err := st.StartCode(svc)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		paste := make(chan string, 1)
		id := s.startGitFlow(svc.ID, paste, func(ctx context.Context, p <-chan string) (string, error) { return flow.Wait(ctx, p) })
		writeJSON(w, http.StatusAccepted, map[string]any{
			"flow_id": id, "method": "code", "service": svc.ID,
			"url": flow.URL, "redirect_uri": flow.RedirectURI(),
		})
	default:
		writeError(w, http.StatusBadRequest, "the method must be device, code or token")
	}
}

// startGitFlow runs a login in the background and registers it so the client can
// poll it.
func (s *Server) startGitFlow(service string, paste chan string, wait func(context.Context, <-chan string) (string, error)) string {
	ctx, cancel := context.WithTimeout(s.baseCtx, gitFlowTTL)
	f := &gitFlow{service: service, created: time.Now(), cancel: cancel, paste: paste, status: "pending"}
	id, _ := newSessionID()
	s.gitMu.Lock()
	if s.gitFlows == nil {
		s.gitFlows = map[string]*gitFlow{}
	}
	for old, o := range s.gitFlows {
		if time.Since(o.created) > 2*gitFlowTTL {
			delete(s.gitFlows, old)
		}
	}
	s.gitFlows[id] = f
	s.gitMu.Unlock()
	go func() {
		defer cancel()
		who, err := wait(ctx, paste)
		s.gitMu.Lock()
		defer s.gitMu.Unlock()
		if err != nil {
			f.status, f.err = "failed", err.Error()
			return
		}
		f.status, f.account = "connected", who
	}()
	return id
}

func (s *Server) flowOf(w http.ResponseWriter, r *http.Request) (*gitFlow, bool) {
	s.gitMu.Lock()
	f := s.gitFlows[r.PathValue("id")]
	s.gitMu.Unlock()
	if f == nil {
		writeError(w, http.StatusNotFound, "there is no login in progress with that id")
	}
	return f, f != nil
}

func (s *Server) handleGitFlow(w http.ResponseWriter, r *http.Request) {
	f, ok := s.flowOf(w, r)
	if !ok {
		return
	}
	s.gitMu.Lock()
	out := map[string]any{"status": f.status, "service": f.service}
	if f.account != "" {
		out["account"] = f.account
	}
	if f.err != "" {
		out["error"] = f.err
	}
	s.gitMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGitFlowPaste(w http.ResponseWriter, r *http.Request) {
	f, ok := s.flowOf(w, r)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if f.paste == nil {
		writeError(w, http.StatusConflict, "this login is completed with the code on the host's page, not by pasting one")
		return
	}
	select {
	case f.paste <- body.Code:
		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusConflict, "a code was already given for this login")
	}
}

func (s *Server) handleGitFlowCancel(w http.ResponseWriter, r *http.Request) {
	f, ok := s.flowOf(w, r)
	if !ok {
		return
	}
	f.cancel()
	s.gitMu.Lock()
	delete(s.gitFlows, r.PathValue("id"))
	s.gitMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitDisconnect(w http.ResponseWriter, r *http.Request) {
	st, ok := s.gitStoreOr(w)
	if !ok {
		return
	}
	svc, found := st.ByID(r.PathValue("id"))
	if !found {
		writeError(w, http.StatusNotFound, "there is no git host with that id")
		return
	}
	if err := st.Remove(svc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitRepos(w http.ResponseWriter, r *http.Request) {
	st, ok := s.gitStoreOr(w)
	if !ok {
		return
	}
	svc, found := st.ByID(r.URL.Query().Get("service"))
	if !found {
		writeError(w, http.StatusBadRequest, "the service must be the id of a git host")
		return
	}
	api, err := st.Open(r.Context(), svc)
	if gitforge.IsNotConnected(err) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": svc.Name + " is not connected", "code": errGitAuthRequired, "service": svc.ID,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	repos, more, err := api.ListRepos(r.Context(), r.URL.Query().Get("q"), page)
	if err != nil {
		var he *gitforge.HTTPError
		if errors.As(err, &he) && he.Unauthorized() {
			// The token was revoked or expired for good: the same answer as no login,
			// which is what makes the dialog offer to connect again.
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": svc.Name + " refused the saved login: connect again", "code": errGitAuthRequired, "service": svc.ID,
			})
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if repos == nil {
		repos = []gitforge.Repo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos, "more": more, "account": api.Cred.Username})
}

// errGitAuthRequired is the machine-readable reason a clone or a listing was
// refused for lack of a login: the client offers to connect, then repeats.
const errGitAuthRequired = "git_auth_required"

// writeCloneError answers a failed clone. A refusal for credentials is not a
// generic bad gateway: it names the host to connect, so the dialog can offer it.
func (s *Server) writeCloneError(w http.ResponseWriter, gitURL string, err error) {
	if isGitAuthFailure(err.Error()) {
		resp := map[string]string{
			"error": "git was not allowed to read this repository: connect your account and try again",
			"code":  errGitAuthRequired,
		}
		if st := s.gitStore(); st != nil {
			if svc, _, ferr := st.ForRemote(gitURL); ferr == nil {
				resp["service"] = svc.ID
			}
		}
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

// isGitAuthFailure recognises git's refusals for credentials. They are matched on
// wording, which git localises, so the variables that make git's output English
// are set by the caller.
func isGitAuthFailure(msg string) bool {
	msg = strings.ToLower(msg)
	for _, marker := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"terminal prompts disabled",
		"http basic: access denied",
		"invalid username or password",
		"repository not found",
		"permission denied (publickey)",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
