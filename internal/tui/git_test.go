package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/i18n"
)

// fakeForgeHTTP is every git host at once: the OAuth endpoints and the REST API of the four
// default hosts, answered from memory, so no test reaches a network.
type fakeForgeHTTP struct {
	mu sync.Mutex
	// tokenBody is what the token endpoint answers; pending forever when it is the pending error.
	tokenBody  string
	deviceFail bool
	repos      int
	reposFail  bool
	whoFail    int
}

const pendingBody = `{"error":"authorization_pending"}`

func (f *fakeForgeHTTP) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, body := 200, "{}"
	path := req.URL.Path
	switch {
	case strings.HasSuffix(path, "/login/device/code"):
		if f.deviceFail {
			status, body = 500, "down"
		} else {
			body = `{"device_code":"dc","user_code":"AB-12","verification_uri":"https://github.com/login/device","expires_in":600,"interval":1}`
		}
	case strings.HasSuffix(path, "/access_token"), strings.HasSuffix(path, "/oauth/token"):
		body = f.tokenBody
		if body == "" {
			body = `{"access_token":"tok"}`
		}
	case strings.HasSuffix(path, "/user/repos"):
		if f.reposFail {
			status, body = 500, `{"message":"boom"}`
			break
		}
		var list []map[string]any
		for i := 0; i < f.repos; i++ {
			list = append(list, map[string]any{"full_name": fmt.Sprintf("octo/r%d", i+1), "private": i == 0})
		}
		data, _ := json.Marshal(list)
		body = string(data)
	case strings.HasSuffix(path, "/user"):
		auth := req.Header.Get("Authorization")
		okBasic := auth == "Basic "+base64.StdEncoding.EncodeToString([]byte("me:good"))
		switch {
		case f.whoFail != 0:
			status, body = f.whoFail, `{"message":"nope"}`
		case auth == "Bearer tok", auth == "token tok", strings.HasSuffix(auth, " good"), okBasic:
			body = `{"login":"octo","username":"octo"}`
		default:
			status, body = 401, `{"message":"Bad credentials"}`
		}
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

// lineScript is the user's side of a login: the lines typed, in order, then silence until the
// flow is abandoned. A step with ok false is the user pressing Esc.
type lineScript struct {
	mu      sync.Mutex
	steps   []scriptStep
	secrets []bool
}

type scriptStep struct {
	line string
	ok   bool
}

func say(lines ...string) []scriptStep {
	var s []scriptStep
	for _, l := range lines {
		s = append(s, scriptStep{l, true})
	}
	return s
}

func (s *lineScript) read(ctx context.Context, secret bool) (string, bool) {
	s.mu.Lock()
	s.secrets = append(s.secrets, secret)
	if len(s.steps) > 0 {
		st := s.steps[0]
		s.steps = s.steps[1:]
		s.mu.Unlock()
		return st.line, st.ok
	}
	s.mu.Unlock()
	<-ctx.Done()
	return "", false
}

type gitRig struct {
	tu     *TUI
	dir    string
	http   *fakeForgeHTTP
	script *lineScript
	opened []string
}

// newGitRig is a TUI whose git side is entirely in memory: a temporary credential directory, the
// fake hosts, a recorded browser and a scripted keyboard. env configures OAuth applications.
func newGitRig(t *testing.T, env map[string]string, steps ...scriptStep) *gitRig {
	t.Helper()
	r := &gitRig{dir: t.TempDir(), http: &fakeForgeHTTP{}, script: &lineScript{steps: steps}}
	r.tu = newFakeTUI("", &fakeRunner{})
	r.tu.Width, r.tu.Height = 120, 40
	r.tu.gitStore = func() gitforge.Store {
		return gitforge.Store{Dir: r.dir, HTTP: r.http, Env: func(k string) string { return env[k] }}
	}
	r.tu.openURL = func(u string) error { r.opened = append(r.opened, u); return nil }
	r.tu.gitLine = r.script.read
	return r
}

func (r *gitRig) say() string { return messagesOf(r.tu) }

func (r *gitRig) connected(id string) bool {
	st := r.tu.forge()
	svc, ok := st.ByID(id)
	return ok && st.Connected(svc)
}

func (r *gitRig) connect(id, token string) {
	st := r.tu.forge()
	svc, _ := st.ByID(id)
	user := ""
	if id == "bitbucket" {
		user = "me"
	}
	if _, err := st.ConnectToken(context.Background(), svc, token, user); err != nil {
		panic(err)
	}
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestGitStatusListsHostsAndAccounts(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.runGit(context.Background(), "")
	mustContain(t, r.say(), "GitHub", "GitLab", "Bitbucket", "Codeberg", "not connected", "/git connect <host>")
	r.connect("github", "good")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "status")
	mustContain(t, r.say(), "connected as octo")
}

func TestGitUnknownSubcommandShowsUsage(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.runGit(context.Background(), "frobnicate")
	mustContain(t, r.say(), "usage: /git")
}

func TestGitDeviceLoginShowsTheCodeOpensTheBrowserAndStores(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.runGit(context.Background(), "connect github")
	mustContain(t, r.say(), "https://github.com/login/device", "AB-12", "connected as octo")
	if len(r.opened) != 1 || r.opened[0] != "https://github.com/login/device" {
		t.Errorf("the browser must be opened on the verification URL: %v", r.opened)
	}
	if !r.connected("github") {
		t.Error("the login must be stored")
	}
	if r.tu.busy {
		t.Error("the interface must not stay busy")
	}
}

func TestGitDeviceStartFailureOffersTheToken(t *testing.T) {
	r := newGitRig(t, nil)
	r.http.deviceFail = true
	r.tu.runGit(context.Background(), "connect github")
	mustContain(t, r.say(), "could not connect GitHub", "/git connect github token")
	if r.connected("github") {
		t.Error("nothing may be stored")
	}
}

func TestGitDeviceLoginIsCancelledWithEsc(t *testing.T) {
	r := newGitRig(t, nil, scriptStep{"", false})
	r.http.tokenBody = pendingBody
	r.tu.runGit(context.Background(), "connect github")
	mustContain(t, r.say(), "cancelled: nothing was stored")
	if r.connected("github") {
		t.Error("a cancelled login stores nothing")
	}
}

func TestGitDeviceLoginStopsWhenTheInterfaceIsCancelled(t *testing.T) {
	r := newGitRig(t, nil)
	r.http.tokenBody = pendingBody
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r.tu.runGit(ctx, "connect github")
	mustContain(t, r.say(), "cancelled: nothing was stored")
	if r.connected("github") {
		t.Error("Ctrl+C stores nothing")
	}
}

func TestGitDeviceRefusedTokenIsReported(t *testing.T) {
	r := newGitRig(t, nil)
	r.http.whoFail = 401
	r.tu.runGit(context.Background(), "connect github")
	mustContain(t, r.say(), "did not accept the token")
	if r.connected("github") {
		t.Error("a refused token is not stored")
	}
}

func TestGitCodeLoginAcceptsAPastedCode(t *testing.T) {
	env := map[string]string{"MOTITA_CODEBERG_CLIENT_ID": "cid", "MOTITA_CODEBERG_CLIENT_SECRET": "sec"}
	r := newGitRig(t, env, say("the-code")...)
	r.tu.runGit(context.Background(), "connect codeberg")
	mustContain(t, r.say(), "https://codeberg.org/login/oauth/authorize", "paste here", "connected as octo")
	if len(r.opened) != 1 || !strings.HasPrefix(r.opened[0], "https://codeberg.org/login/oauth/authorize") {
		t.Errorf("opened %v", r.opened)
	}
	if !r.connected("codeberg") {
		t.Error("the login must be stored")
	}
}

func TestGitCodeLoginIsCancelledAndReleasesTheListener(t *testing.T) {
	env := map[string]string{"MOTITA_CODEBERG_CLIENT_ID": "cid", "MOTITA_CODEBERG_CLIENT_SECRET": "sec"}
	r := newGitRig(t, env, scriptStep{"", false})
	r.tu.runGit(context.Background(), "connect codeberg")
	mustContain(t, r.say(), "cancelled: nothing was stored")
	if r.connected("codeberg") {
		t.Error("nothing may be stored")
	}
}

func TestGitCodeWithoutAnOAuthApplicationIsExplained(t *testing.T) {
	r := newGitRig(t, nil)
	svc, _ := r.tu.forge().ByID("codeberg")
	_, err := r.tu.gitCode(context.Background(), r.tu.forge(), svc)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_CODEBERG_CLIENT_ID") {
		t.Errorf("err = %v", err)
	}
}

func TestGitTokenLoginReadsTheTokenWithoutEcho(t *testing.T) {
	r := newGitRig(t, nil, say("good")...)
	r.tu.runGit(context.Background(), "connect github token")
	mustContain(t, r.say(), "https://github.com/settings/tokens", "Paste the token", "connected as octo")
	if len(r.script.secrets) != 1 || !r.script.secrets[0] {
		t.Errorf("the token must be read as a secret: %v", r.script.secrets)
	}
	if !r.connected("github") {
		t.Error("stored")
	}
}

func TestGitTokenThatTheHostRefusesIsNotStored(t *testing.T) {
	r := newGitRig(t, nil, say("bad")...)
	r.tu.runGit(context.Background(), "connect github token")
	mustContain(t, r.say(), "did not accept the token")
	if r.connected("github") {
		t.Error("not stored")
	}
}

func TestGitTokenCancelled(t *testing.T) {
	r := newGitRig(t, nil, scriptStep{"", false})
	r.tu.runGit(context.Background(), "connect github token")
	mustContain(t, r.say(), "cancelled: nothing was stored")
}

func TestGitBitbucketAsksForTheAccountThenTheAppPassword(t *testing.T) {
	r := newGitRig(t, nil, say("me", "good")...)
	r.tu.runGit(context.Background(), "connect bitbucket")
	mustContain(t, r.say(), "One-click login for Bitbucket needs MOTITA_BITBUCKET_CLIENT_ID", "Your Bitbucket account name", "connected as me")
	if got := r.script.secrets; len(got) != 2 || got[0] || !got[1] {
		t.Errorf("the account is plain and the password secret: %v", got)
	}
}

func TestGitBitbucketCancelledAtEitherPrompt(t *testing.T) {
	r := newGitRig(t, nil, scriptStep{"", false})
	r.tu.runGit(context.Background(), "connect bitbucket")
	mustContain(t, r.say(), "cancelled: nothing was stored")
	r = newGitRig(t, nil, append(say("me"), scriptStep{"", false})...)
	r.tu.runGit(context.Background(), "connect bitbucket")
	mustContain(t, r.say(), "cancelled: nothing was stored")
	if r.connected("bitbucket") {
		t.Error("nothing stored")
	}
}

func TestGitConnectPicker(t *testing.T) {
	r := newGitRig(t, nil, say("1")...)
	r.http.tokenBody = `{"access_token":"tok"}`
	r.tu.runGit(context.Background(), "connect")
	mustContain(t, r.say(), "Connect to which git host?", "1) GitHub", "4) Codeberg", "connected as octo")

	// A name works as well as a number, and a bad answer asks again.
	r = newGitRig(t, nil, say("9", "nowhere", "github")...)
	r.tu.runGit(context.Background(), "connect")
	mustContain(t, r.say(), "unknown git host \"nowhere\"", "connected as octo")

	// An empty answer and Esc both leave.
	for _, step := range []scriptStep{{"", true}, {"", false}} {
		r = newGitRig(t, nil, step)
		r.tu.runGit(context.Background(), "connect")
		mustContain(t, r.say(), "cancelled: nothing was stored")
	}

	// "token" alone still asks which host.
	r = newGitRig(t, nil, say("github", "good")...)
	r.tu.runGit(context.Background(), "connect token")
	mustContain(t, r.say(), "Paste the token", "connected as octo")
}

func TestGitSelfHostedHost(t *testing.T) {
	r := newGitRig(t, nil, say("good", "good")...)
	r.tu.runGit(context.Background(), "connect gitlab:git.corp.io")
	mustContain(t, r.say(), "https://git.corp.io/", "One-click login for GitLab (git.corp.io) needs MOTITA_GITLAB_CLIENT_ID", "connected as octo")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "connect forgejo:code.example.org")
	mustContain(t, r.say(), "Gitea (code.example.org)")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "connect gitlab:bad host!")
	mustContain(t, r.say(), "is not a host name")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "connect nowhere")
	mustContain(t, r.say(), "unknown git host \"nowhere\"")
}

func TestGitHostMatchesIDHostAndName(t *testing.T) {
	r := newGitRig(t, nil)
	for _, arg := range []string{"gitlab", "gitlab.com", "GitLab"} {
		svc, err := r.tu.gitHost(arg)
		if err != nil || svc.ID != "gitlab" {
			t.Errorf("%q: %v %v", arg, svc.ID, err)
		}
	}
}

func TestGitDisconnect(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.runGit(context.Background(), "disconnect")
	mustContain(t, r.say(), "usage: /git disconnect <host>")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "disconnect nowhere")
	mustContain(t, r.say(), "unknown git host")
	r.tu.messages = nil
	r.tu.runGit(context.Background(), "disconnect github")
	mustContain(t, r.say(), "GitHub is not connected.")
	r.tu.messages = nil
	r.connect("github", "good")
	r.tu.runGit(context.Background(), "disconnect github")
	mustContain(t, r.say(), "GitHub disconnected.")
	if r.connected("github") {
		t.Error("the login must be gone")
	}
}

func TestGitDisconnectReportsAFailureToRemove(t *testing.T) {
	r := newGitRig(t, nil)
	blocker := filepath.Join(r.dir, "git-github.json")
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.tu.runGit(context.Background(), "disconnect github")
	mustContain(t, r.say(), "could not disconnect GitHub")
}

func TestGitReposNeedsAConnection(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.runGit(context.Background(), "repos")
	mustContain(t, r.say(), "Type /git connect first")
}

func TestGitReposListsNumberedRepositories(t *testing.T) {
	r := newGitRig(t, nil)
	r.http.repos = 3
	r.connect("github", "good")
	r.tu.runGit(context.Background(), "repos")
	mustContain(t, r.say(), "GitHub (octo)", " 1. octo/r1  private", " 3. octo/r3")
	if strings.Contains(r.say(), "narrow the list") {
		t.Error("a short list is complete")
	}
}

func TestGitReposTruncatesALongList(t *testing.T) {
	r := newGitRig(t, nil)
	r.http.repos = gitReposPageLimit + 5
	r.connect("github", "good")
	r.tu.runGit(context.Background(), "repos r")
	mustContain(t, r.say(), strconv.Itoa(gitReposPageLimit)+". octo/r"+strconv.Itoa(gitReposPageLimit), "narrow the list with /git repos <query>")
	if strings.Contains(r.say(), "octo/r"+strconv.Itoa(gitReposPageLimit+1)+"\n") {
		t.Error("the list is capped")
	}
}

func TestGitReposEmptyAndFailing(t *testing.T) {
	r := newGitRig(t, nil)
	r.connect("github", "good")
	r.tu.runGit(context.Background(), "repos")
	mustContain(t, r.say(), "no repositories found")
	r.tu.messages = nil
	r.http.reposFail = true
	r.tu.runGit(context.Background(), "repos")
	mustContain(t, r.say(), "could not list the repositories")
}

func TestGitNudge(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.announceGit()
	r.tu.announceGit()
	if got := r.say(); strings.Count(got, "No git host is connected.") != 1 || !strings.Contains(got, "/git connects GitHub, GitLab or Bitbucket") {
		t.Errorf("the nudge is said once:\n%s", got)
	}

	r = newGitRig(t, nil)
	r.connect("gitlab", "good")
	r.tu.announceGit()
	if len(r.tu.messages) != 0 {
		t.Errorf("a connected host silences the nudge: %v", r.say())
	}
}

func TestGitNudgeIsPartOfStartingTheInterface(t *testing.T) {
	r := newGitRig(t, nil)
	r.tu.gitNudge = true
	r.tu.In = strings.NewReader("/quit\n")
	if code := r.tu.Run(context.Background()); code != ExitSuccess {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, r.say(), "No git host is connected.")

	quiet := newGitRig(t, nil)
	quiet.tu.In = strings.NewReader("/quit\n")
	quiet.tu.Run(context.Background())
	if strings.Contains(quiet.say(), "No git host") {
		t.Error("a TUI built without the nudge stays quiet")
	}
}

func TestGitSlashCommandRuns(t *testing.T) {
	r := newGitRig(t, nil)
	if quit := commandActions["/git"](r.tu, context.Background(), "status"); quit {
		t.Error("/git never quits")
	}
	mustContain(t, r.say(), "not connected")
}

func TestForgeDefaultsToTheAuthDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTITA_AUTH_DIR", dir)
	tu := newFakeTUI("", &fakeRunner{})
	if got := tu.forge().Dir; got != dir {
		t.Errorf("Dir = %q", got)
	}
}

func TestGitLineReadsTypedTextAndMasksSecrets(t *testing.T) {
	tu, out := liveTUI("hunter2\n")
	line, ok := tu.gitLineIn(context.Background(), true)
	if !ok || line != "hunter2" {
		t.Fatalf("line = %q %v", line, ok)
	}
	if strings.Contains(stripANSI(out.String()), "hunter2") {
		t.Error("a secret must never be drawn")
	}
	if !strings.Contains(out.String(), "•") {
		t.Error("the secret is drawn as bullets")
	}
	if tu.prompting || tu.maskInput {
		t.Error("the prompt state must be reset")
	}

	// A plain answer is drawn as typed, and keys that are not text are skipped.
	tu, out = liveTUI("\x1b[Aabc\n")
	if line, ok := tu.gitLineIn(context.Background(), false); !ok || line != "abc" {
		t.Fatalf("line = %q %v", line, ok)
	}
	if !strings.Contains(stripANSI(out.String()), "abc") {
		t.Error("a plain answer is drawn")
	}
}

func TestGitLineEscAndClosedInputCancel(t *testing.T) {
	tu, _ := liveTUI("\x1b")
	if _, ok := tu.gitLineIn(context.Background(), false); ok {
		t.Error("Esc cancels")
	}
	tu, _ = liveTUI("")
	if _, ok := tu.gitLineIn(context.Background(), false); ok {
		t.Error("a closed input cancels")
	}
}

func TestGitLineInWholeLineMode(t *testing.T) {
	tu := newFakeTUI("typed\n", &fakeRunner{})
	if line, ok := tu.gitLineIn(context.Background(), false); !ok || line != "typed" {
		t.Errorf("line = %q %v", line, ok)
	}
}

func TestShownDraftAndCompletionWhilePrompting(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	tu.draft = "/pl"
	if !tu.completing() {
		t.Fatal("a command being typed completes")
	}
	tu.prompting = true
	if tu.completing() {
		t.Error("an answer to a prompt is not a command")
	}
	tu.maskInput = true
	if got := tu.shownDraft(); got != "•••" {
		t.Errorf("shownDraft = %q", got)
	}
	tu.maskInput = false
	if got := tu.shownDraft(); got != "/pl" {
		t.Errorf("shownDraft = %q", got)
	}
}

func TestOpenBrowserUsesTheOperatingSystemsOpener(t *testing.T) {
	for goos, want := range map[string]string{"linux": "xdg-open", "darwin": "open", "windows": "rundll32", "freebsd": "xdg-open"} {
		name, args := openCommand(goos, "http://x")
		if name != want || args[len(args)-1] != "http://x" {
			t.Errorf("%s: %s %v", goos, name, args)
		}
	}

	var started []string
	old := startProcess
	startProcess = func(name string, args ...string) error {
		started = append(started, name+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { startProcess = old }()
	tu := newFakeTUI("", &fakeRunner{})
	tu.openBrowser("http://x")
	if len(started) != 1 || !strings.Contains(started[0], "http://x") {
		t.Errorf("started %v", started)
	}

	t.Setenv("MOTITA_NO_BROWSER", "1")
	tu.openBrowser("http://y")
	if len(started) != 1 {
		t.Errorf("MOTITA_NO_BROWSER must stop the opener: %v", started)
	}
}

func TestStartProcessStartsAndReportsFailure(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if err := startProcess(exe, "-test.run=^$"); err != nil {
		t.Errorf("a real program starts: %v", err)
	}
	if err := startProcess(filepath.Join(t.TempDir(), "no-such-program")); err == nil {
		t.Error("a missing program is an error")
	}
}

func TestGitAuthFailuresGetAHint(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	for _, msg := range []string{
		"fatal: Authentication failed for 'https://github.com/o/r.git/'",
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"git@github.com: Permission denied (publickey).",
		"remote: HTTP Basic: Access denied",
	} {
		got := tu.errorText(errors.New(msg))
		mustContain(t, got, "error: "+msg, "hint: git could not sign in", "/git connect")
	}
	tu.Lang = i18n.ES
	mustContain(t, tu.errorText(errors.New("Authentication failed")), "pista: git no pudo iniciar sesión") // spanish-fixture: the Spanish hint
}

// TestEveryGitStringHasASpanishTranslation reads git.go and checks that every string passed to
// tr or trf, and the catalogue entries of /git, are in the Spanish catalogue: a string added
// without its translation would show English to a Spanish user, and nothing else would notice.
func TestEveryGitStringHasASpanishTranslation(t *testing.T) {
	fset := token.NewFileSet()
	_, here, _, _ := runtime.Caller(0)
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(here), "git.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{gitNudgeText: true}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "tr" && sel.Sel.Name != "trf") || len(call.Args) == 0 {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			keys[s] = true
		}
		return true
	})
	for _, c := range commands {
		if c.Name == "/git" {
			keys[c.Help], keys[c.Arg] = true, true
		}
	}
	keys["hint: git could not sign in to the host. Type /git connect to connect GitHub, GitLab or Bitbucket."] = true
	if len(keys) < 25 {
		t.Fatalf("only %d strings found: the scan is broken", len(keys))
	}
	for k := range keys {
		if i18n.T(i18n.ES, k) == k && k != "[connect]" {
			t.Errorf("no Spanish for %q", k)
		}
	}
}
