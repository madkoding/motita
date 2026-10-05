package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// gitHost fakes every git host and OAuth server by "METHOD url-prefix".
type gitHost struct {
	mu     sync.Mutex
	routes map[string]string
	seen   []string
}

func (g *gitHost) Do(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()
	g.mu.Lock()
	g.seen = append(g.seen, key)
	routes := map[string]string{}
	for k, v := range g.routes {
		routes[k] = v
	}
	g.mu.Unlock()
	best, body := "", ""
	for pattern, b := range routes {
		if strings.HasPrefix(key, pattern) && len(pattern) > len(best) {
			best, body = pattern, b
		}
	}
	if best == "" {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"message":"no route"}`)), Header: http.Header{}}, nil
	}
	status := 200
	if rest, ok := strings.CutPrefix(body, "STATUS "); ok {
		code, payload, _ := strings.Cut(rest, " ")
		status = map[string]int{"401": 401, "500": 500}[code]
		body = payload
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func (g *gitHost) set(k, v string) {
	g.mu.Lock()
	g.routes[k] = v
	g.mu.Unlock()
}

func gitServer(t *testing.T) (*Server, *gitHost) {
	t.Helper()
	host := &gitHost{routes: map[string]string{
		"GET https://api.github.com/user": `{"login":"octo"}`,
	}}
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.GitAuthDir = t.TempDir()
		o.GitHTTP = host
	})
	return srv, host
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %q", w.Body.String())
	}
	return m
}

func TestGitEndpointsNeedAStore(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, c := range [][2]string{
		{"GET", "/v1/git/accounts"}, {"POST", "/v1/git/connect"},
		{"DELETE", "/v1/git/accounts/github"}, {"GET", "/v1/git/repos?service=github"},
	} {
		if w := send(t, srv, c[0], c[1], testToken, `{}`); w.Code != http.StatusNotImplemented {
			t.Errorf("%v: status %d", c, w.Code)
		}
	}
	if w := get(t, srv, "/v1/git/accounts", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the git endpoints are behind the token: %d", w.Code)
	}
}

func TestGitAccountsAndTokenConnection(t *testing.T) {
	srv, _ := gitServer(t)
	w := get(t, srv, "/v1/git/accounts", testToken)
	accounts := decode(t, w)["accounts"].([]any)
	if len(accounts) != 3 {
		t.Fatalf("accounts = %v", accounts)
	}
	byID := map[string]map[string]any{}
	for _, a := range accounts {
		m := a.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["github"]["connected"] != false || byID["github"]["oauth_ready"] != true || byID["github"]["setup_hint"] != nil {
		t.Errorf("github = %v", byID["github"])
	}
	if byID["gitlab"]["oauth_ready"] != false || !strings.Contains(byID["gitlab"]["setup_hint"].(string), "MOTITA_GITLAB_CLIENT_ID") {
		t.Errorf("gitlab = %v", byID["gitlab"])
	}

	w = postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","token":"gho_x"}`)
	if w.Code != http.StatusOK || decode(t, w)["account"] != "octo" {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	accounts = decode(t, get(t, srv, "/v1/git/accounts", testToken))["accounts"].([]any)
	if gh := accounts[0].(map[string]any); gh["connected"] != true || gh["username"] != "octo" {
		t.Errorf("github = %v", gh)
	}
	if w := del(t, srv, "/v1/git/accounts/github", testToken); w.Code != http.StatusNoContent {
		t.Errorf("disconnect: %d", w.Code)
	}
	if w := del(t, srv, "/v1/git/accounts/nope", testToken); w.Code != http.StatusNotFound {
		t.Errorf("disconnect unknown: %d", w.Code)
	}
}

func TestGitConnectRefusals(t *testing.T) {
	srv, host := gitServer(t)
	host.set("GET https://api.github.com/user", `STATUS 401 {"message":"Bad credentials"}`)
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"bad json":       {`nope`, 400},
		"unknown id":     {`{"service":"nope"}`, 400},
		"bad host":       {`{"kind":"gitlab","host":"a/b"}`, 400},
		"unsupported":    {`{"kind":"bitbucket","host":"bb.corp.io"}`, 400},
		"bad method":     {`{"service":"github","method":"carrier-pigeon"}`, 400},
		"rejected token": {`{"service":"github","token":"bad"}`, 400},
		"bitbucket user": {`{"service":"bitbucket","token":"pw"}`, 400},
		"gitlab device":  {`{"service":"gitlab","method":"device"}`, 502},
		"bitbucket code": {`{"service":"bitbucket","method":"code"}`, 502},
	} {
		if w := postJSON(t, srv, "/v1/git/connect", testToken, tc.body); w.Code != tc.want {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body.String())
		}
	}
}

func waitFlow(t *testing.T, srv *Server, id, want string) map[string]any {
	t.Helper()
	for i := 0; i < 500; i++ {
		m := decode(t, get(t, srv, "/v1/git/flows/"+id, testToken))
		if m["status"] == want {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the flow never became %s", want)
	return nil
}

func TestGitDeviceLogin(t *testing.T) {
	srv, host := gitServer(t)
	host.set("POST https://github.com/login/device/code", `{"device_code":"dc","user_code":"AB-12","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
	host.set("POST https://github.com/login/oauth/access_token", `{"access_token":"gho_dev","refresh_token":""}`)
	w := postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github"}`)
	m := decode(t, w)
	if w.Code != http.StatusAccepted || m["method"] != "device" || m["code"] != "AB-12" || m["url"] != "https://github.com/login/device" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	done := waitFlow(t, srv, m["flow_id"].(string), "connected")
	if done["account"] != "octo" {
		t.Errorf("done = %v", done)
	}
	// A device flow cannot be completed by pasting.
	if w := postJSON(t, srv, "/v1/git/flows/"+m["flow_id"].(string)+"/paste", testToken, `{"code":"x"}`); w.Code != http.StatusConflict {
		t.Errorf("paste on a device flow: %d", w.Code)
	}
}

func TestGitDeviceLoginFailsAndCancels(t *testing.T) {
	srv, host := gitServer(t)
	host.set("POST https://github.com/login/device/code", `{"device_code":"dc","user_code":"AB-12","verification_uri":"u","interval":1}`)
	host.set("POST https://github.com/login/oauth/access_token", `{"error":"access_denied"}`)
	m := decode(t, postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","method":"device"}`))
	failed := waitFlow(t, srv, m["flow_id"].(string), "failed")
	if !strings.Contains(failed["error"].(string), "refused") {
		t.Errorf("failed = %v", failed)
	}
	// A pending login can be cancelled, and is then gone.
	host.set("POST https://github.com/login/oauth/access_token", `{"error":"authorization_pending"}`)
	m = decode(t, postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github"}`))
	id := m["flow_id"].(string)
	if got := decode(t, get(t, srv, "/v1/git/flows/"+id, testToken)); got["status"] != "pending" {
		t.Errorf("status = %v", got)
	}
	if w := del(t, srv, "/v1/git/flows/"+id, testToken); w.Code != http.StatusNoContent {
		t.Errorf("cancel: %d", w.Code)
	}
	if w := get(t, srv, "/v1/git/flows/"+id, testToken); w.Code != http.StatusNotFound {
		t.Errorf("a cancelled flow is gone: %d", w.Code)
	}
	for _, c := range [][2]string{{"GET", "/v1/git/flows/nope"}, {"DELETE", "/v1/git/flows/nope"}, {"POST", "/v1/git/flows/nope/paste"}} {
		if w := send(t, srv, c[0], c[1], testToken, `{"code":"x"}`); w.Code != http.StatusNotFound {
			t.Errorf("%v: %d", c, w.Code)
		}
	}
}

func TestGitCodeLoginByPaste(t *testing.T) {
	t.Setenv("MOTITA_GITEA_CLIENT_ID", "cid")
	t.Setenv("MOTITA_GITEA_CLIENT_SECRET", "sec")
	srv, host := gitServer(t)
	host.set("POST https://forge.test/login/oauth/access_token", `{"access_token":"gta_tok"}`)
	host.set("GET https://forge.test/api/v1/user", `{"login":"gitea-user"}`)
	w := postJSON(t, srv, "/v1/git/connect", testToken, `{"kind":"gitea","host":"forge.test"}`)
	m := decode(t, w)
	if w.Code != http.StatusAccepted || m["method"] != "code" || !strings.Contains(m["url"].(string), "https://forge.test/login/oauth/authorize?") || !strings.HasPrefix(m["redirect_uri"].(string), "http://localhost:") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	id := m["flow_id"].(string)
	if w := postJSON(t, srv, "/v1/git/flows/"+id+"/paste", testToken, `{"code":"abc"}`); w.Code != http.StatusAccepted {
		t.Fatalf("paste: %d %s", w.Code, w.Body.String())
	}
	// A second code for the same login is refused.
	if w := postJSON(t, srv, "/v1/git/flows/"+id+"/paste", testToken, `{"code":"abc"}`); w.Code != http.StatusConflict && w.Code != http.StatusAccepted {
		t.Errorf("second paste: %d", w.Code)
	}
	if done := waitFlow(t, srv, id, "connected"); done["account"] != "gitea-user" {
		t.Errorf("done = %v", done)
	}
	if w := postJSON(t, srv, "/v1/git/flows/"+id+"/paste", testToken, `nope`); w.Code != http.StatusBadRequest {
		t.Errorf("a malformed paste: %d", w.Code)
	}
}

func TestGitFlowsAreSweptWhenOld(t *testing.T) {
	srv, host := gitServer(t)
	host.set("POST https://github.com/login/device/code", `{"device_code":"dc","user_code":"U","verification_uri":"u","interval":1}`)
	host.set("POST https://github.com/login/oauth/access_token", `{"error":"authorization_pending"}`)
	first := decode(t, postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github"}`))["flow_id"].(string)
	srv.gitMu.Lock()
	srv.gitFlows[first].created = time.Now().Add(-3 * gitFlowTTL)
	srv.gitMu.Unlock()
	decode(t, postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github"}`))
	srv.gitMu.Lock()
	_, kept := srv.gitFlows[first]
	srv.gitMu.Unlock()
	if kept {
		t.Error("an abandoned login must be dropped")
	}
}

func TestGitRepos(t *testing.T) {
	srv, host := gitServer(t)
	if w := get(t, srv, "/v1/git/repos?service=nope", testToken); w.Code != http.StatusBadRequest {
		t.Errorf("unknown service: %d", w.Code)
	}
	w := get(t, srv, "/v1/git/repos?service=github", testToken)
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != "git_auth_required" || m["service"] != "github" {
		t.Fatalf("not connected: %d %s", w.Code, w.Body.String())
	}
	postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","token":"gho_x"}`)
	host.set("GET https://api.github.com/user/repos", `[{"full_name":"octo/app","clone_url":"https://github.com/octo/app.git","private":true}]`)
	w = get(t, srv, "/v1/git/repos?service=github&q=app&page=2", testToken)
	m := decode(t, w)
	repos := m["repos"].([]any)
	if w.Code != http.StatusOK || len(repos) != 1 || repos[0].(map[string]any)["full_name"] != "octo/app" || m["account"] != "octo" || m["more"] != false {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// An empty account answers an empty list, not null.
	host.set("GET https://api.github.com/user/repos", `[]`)
	if w = get(t, srv, "/v1/git/repos?service=github", testToken); !strings.Contains(w.Body.String(), `"repos":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
	// The host revoked the token: the dialog is told to connect again.
	host.set("GET https://api.github.com/user/repos", `STATUS 401 {"message":"Bad credentials"}`)
	if w = get(t, srv, "/v1/git/repos?service=github", testToken); w.Code != http.StatusConflict || decode(t, w)["code"] != "git_auth_required" {
		t.Errorf("revoked: %d %s", w.Code, w.Body.String())
	}
	host.set("GET https://api.github.com/user/repos", `STATUS 500 {"message":"down"}`)
	if w = get(t, srv, "/v1/git/repos?service=github", testToken); w.Code != http.StatusBadGateway {
		t.Errorf("host down: %d", w.Code)
	}
}

func TestGitReposStoreFailure(t *testing.T) {
	srv, _ := gitServer(t)
	// A login file that cannot be read is a server error, not "not connected".
	w := postJSON(t, srv, "/v1/git/connect", testToken, `{"service":"github","token":"gho_x"}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	corrupt(t, srv.opts.GitAuthDir, "git-github.json")
	if w := get(t, srv, "/v1/git/repos?service=github", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("corrupt login: %d", w.Code)
	}
}

func TestGitDisconnectFailure(t *testing.T) {
	srv, _ := gitServer(t)
	// A non-empty directory where the login file should be makes the removal fail.
	mkdirAt(t, srv.opts.GitAuthDir, "git-github.json")
	if err := os.WriteFile(srv.opts.GitAuthDir+"/git-github.json/keep", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if w := del(t, srv, "/v1/git/accounts/github", testToken); w.Code != http.StatusInternalServerError {
		t.Errorf("status %d", w.Code)
	}
}

func TestWriteCloneError(t *testing.T) {
	srv, _ := gitServer(t)
	w := httptest.NewRecorder()
	srv.writeCloneError(w, "https://github.com/o/r.git", errors.New("fatal: Authentication failed for 'https://github.com/o/r.git/'"))
	if m := decode(t, w); w.Code != http.StatusConflict || m["code"] != "git_auth_required" || m["service"] != "github" {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
	// An address no host is known for still gets the code, without a service.
	w = httptest.NewRecorder()
	srv.writeCloneError(w, "https://example.org/o/r.git", errors.New("fatal: could not read Username"))
	if m := decode(t, w); w.Code != http.StatusConflict || m["service"] != nil {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
	// A gateway with no store cannot name the host either.
	bare := newTestServer(t, &fakeService{})
	w = httptest.NewRecorder()
	bare.writeCloneError(w, "https://github.com/o/r.git", errors.New("fatal: Authentication failed"))
	if m := decode(t, w); w.Code != http.StatusConflict || m["service"] != nil {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
	// Anything else is a bad gateway.
	w = httptest.NewRecorder()
	srv.writeCloneError(w, "https://github.com/o/r.git", errors.New("fatal: unable to access: Could not resolve host"))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status %d", w.Code)
	}
}

func TestIsGitAuthFailure(t *testing.T) {
	for _, yes := range []string{
		"fatal: Authentication failed for 'x'", "fatal: could not read Username for 'https://x': terminal prompts disabled",
		"remote: HTTP Basic: Access denied", "remote: Invalid username or password.", "ERROR: Repository not found.",
		"git@github.com: Permission denied (publickey).", "fatal: could not read Password",
	} {
		if !isGitAuthFailure(yes) {
			t.Errorf("%q must be an auth failure", yes)
		}
	}
	for _, no := range []string{"", "fatal: unable to access 'x': Could not resolve host", "fatal: destination path exists"} {
		if isGitAuthFailure(no) {
			t.Errorf("%q must not be an auth failure", no)
		}
	}
}

func TestGitCommandEnv(t *testing.T) {
	bare := newTestServer(t, &fakeService{})
	env := strings.Join(bare.gitCommandEnv(), "\n")
	if !strings.Contains(env, "LC_ALL=C") || strings.Contains(env, "credential.helper") {
		t.Errorf("a gateway with no logins only forces English: %s", env)
	}
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.ExePath = "/opt/motita"; o.GitAuthDir = "/auth" })
	env = strings.Join(srv.gitCommandEnv(), "\n")
	for _, want := range []string{"LC_ALL=C", "MOTITA_AUTH_DIR=/auth", "'/opt/motita' git-credential"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestWithoutInlineGitConfig(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=!/opt/motita git-credential",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=/home/u",
	}
	got := strings.Join(withoutInlineGitConfig(env), "\n")
	want := "PATH=/usr/bin\nGIT_CONFIG_NOSYSTEM=1\nHOME=/home/u"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func corrupt(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdirAt(t *testing.T, dir, name string) {
	t.Helper()
	_ = os.Remove(dir + "/" + name)
	if err := os.Mkdir(dir+"/"+name, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestGitSecondPasteIsRefused(t *testing.T) {
	srv, _ := gitServer(t)
	srv.gitFlows = map[string]*gitFlow{"f": {paste: make(chan string, 1), status: "pending", cancel: func() {}}}
	if w := postJSON(t, srv, "/v1/git/flows/f/paste", testToken, `{"code":"one"}`); w.Code != http.StatusAccepted {
		t.Fatalf("first paste: %d", w.Code)
	}
	if w := postJSON(t, srv, "/v1/git/flows/f/paste", testToken, `{"code":"two"}`); w.Code != http.StatusConflict {
		t.Errorf("second paste: %d", w.Code)
	}
}
