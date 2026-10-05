package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitforge"
)

// gitNudgeText is the one sentence shown when the interface opens with no git host connected.
const gitNudgeText = "No git host is connected. /git connects GitHub, GitLab or Bitbucket so motita can clone, push and open pull requests for you."

// gitReposPageLimit bounds how many repositories /git repos prints per host: it is a list to see
// what is reachable, not a browser, and a query narrows it.
const gitReposPageLimit = 30

// forge is the store of git logins: the injected one, or the real one on the auth directory.
func (t *TUI) forge() gitforge.Store {
	if t.gitStore != nil {
		return t.gitStore()
	}
	return gitforge.Store{Dir: config.AuthDir()}
}

// announceGit is the startup nudge. It reads the credential files only (no network), says
// nothing when any host is connected, and speaks once per interface.
func (t *TUI) announceGit() {
	if t.gitNudged {
		return
	}
	t.gitNudged = true
	for _, a := range t.forge().Accounts() {
		if a.Connected {
			return
		}
	}
	t.addMessage(AuthorSystem, t.tr(gitNudgeText))
}

// runGit is /git: status, connect, disconnect and repos.
func (t *TUI) runGit(ctx context.Context, arg string) {
	fields := strings.Fields(arg)
	sub, rest := "status", ""
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(arg), fields[0]))
	}
	switch sub {
	case "status", "list":
		t.gitStatus()
	case "connect", "login":
		t.gitConnect(ctx, rest)
	case "disconnect", "logout":
		t.gitDisconnect(rest)
	case "repos":
		t.gitRepos(ctx, rest)
	default:
		t.addMessage(AuthorSystem, t.tr("usage: /git [status | connect [host] [token] | disconnect <host> | repos [query]]"))
	}
}

// gitStatus lists every host and who the user is on it.
func (t *TUI) gitStatus() {
	var b strings.Builder
	accounts := t.forge().Accounts()
	width := 0
	for _, a := range accounts {
		if n := len(a.Service.Name); n > width {
			width = n
		}
	}
	for _, a := range accounts {
		state := t.tr("not connected")
		if a.Connected {
			state = t.trf("connected as %s", a.Username)
		}
		fmt.Fprintf(&b, "%-*s  %s\n", width, a.Service.Name, state)
	}
	b.WriteString("\n" + t.tr("/git connect <host> signs in; self-hosted: /git connect gitlab:git.example.com"))
	t.addPreformatted(AuthorSystem, b.String())
}

// gitHost resolves what the user typed into a service: an id or host of a known one
// ("github", "gitlab.com"), or kind:host for a self-hosted server.
func (t *TUI) gitHost(arg string) (gitforge.Service, error) {
	st := t.forge()
	arg = strings.ToLower(strings.TrimSpace(arg))
	if kind, host, ok := strings.Cut(arg, ":"); ok {
		if kind == "forgejo" {
			kind = string(gitforge.KindGitea)
		}
		return gitforge.Custom(gitforge.Kind(kind), host, st.Env)
	}
	for _, svc := range st.All() {
		if arg == svc.ID || arg == svc.Host || arg == strings.ToLower(svc.Name) {
			return svc, nil
		}
	}
	return gitforge.Service{}, fmt.Errorf("%s", t.trf("unknown git host %q: use github, gitlab, bitbucket, codeberg or kind:host (gitlab:git.example.com)", arg))
}

// gitDisconnect forgets a host's login.
func (t *TUI) gitDisconnect(arg string) {
	if strings.TrimSpace(arg) == "" {
		t.addMessage(AuthorSystem, t.tr("usage: /git disconnect <host>"))
		return
	}
	svc, err := t.gitHost(arg)
	if err != nil {
		t.addMessage(AuthorSystem, err.Error())
		return
	}
	st := t.forge()
	was := st.Connected(svc)
	if err := st.Remove(svc); err != nil {
		t.addMessage(AuthorSystem, t.trf("could not disconnect %s: %v", svc.Name, err))
		return
	}
	if !was {
		t.addMessage(AuthorSystem, t.trf("%s is not connected.", svc.Name))
		return
	}
	t.addMessage(AuthorSystem, t.trf("%s disconnected.", svc.Name))
}

// gitRepos lists the repositories the connected accounts can reach.
func (t *TUI) gitRepos(ctx context.Context, query string) {
	st := t.forge()
	var b strings.Builder
	n, connected := 0, 0
	for _, a := range st.Accounts() {
		if !a.Connected {
			continue
		}
		connected++
		api, err := st.Open(ctx, a.Service)
		var repos []gitforge.Repo
		var more bool
		if err == nil {
			repos, more, err = api.ListRepos(ctx, query, 1)
		}
		fmt.Fprintf(&b, "%s (%s)\n", a.Service.Name, a.Username)
		if err != nil {
			fmt.Fprintf(&b, "  %s\n", t.trf("could not list the repositories: %v", err))
			continue
		}
		if len(repos) == 0 {
			fmt.Fprintf(&b, "  %s\n", t.tr("no repositories found"))
		}
		for i, r := range repos {
			if i == gitReposPageLimit {
				more = true
				break
			}
			n++
			vis := ""
			if r.Private {
				vis = "  " + t.tr("private")
			}
			fmt.Fprintf(&b, "  %2d. %s%s\n", n, r.FullName, vis)
		}
		if more {
			fmt.Fprintf(&b, "  %s\n", t.tr("… more: narrow the list with /git repos <query>"))
		}
	}
	if connected == 0 {
		t.addMessage(AuthorSystem, t.tr("No git host is connected. Type /git connect first."))
		return
	}
	t.addPreformatted(AuthorSystem, strings.TrimRight(b.String(), "\n"))
}

// gitConnect signs in to a host, choosing the best way the host offers.
func (t *TUI) gitConnect(ctx context.Context, arg string) {
	var svc gitforge.Service
	var err error
	// "token" as the last word forces the token way, for a host whose one-click login is blocked.
	forceToken := false
	if f := strings.Fields(arg); len(f) > 0 && strings.EqualFold(f[len(f)-1], "token") {
		forceToken = true
		arg = strings.Join(f[:len(f)-1], " ")
	}
	if strings.TrimSpace(arg) == "" {
		var ok bool
		if svc, ok = t.gitPick(ctx); !ok {
			return
		}
	} else if svc, err = t.gitHost(arg); err != nil {
		t.addMessage(AuthorSystem, err.Error())
		return
	}
	st := t.forge()
	method := svc.Methods()[0]
	if forceToken {
		method = gitforge.MethodToken
	} else if method == gitforge.MethodToken {
		t.addMessage(AuthorSystem, t.trf("One-click login for %s needs %s_CLIENT_ID; connecting with a token instead.", svc.Name, svc.EnvPrefix))
	}
	var user string
	switch method {
	case gitforge.MethodDevice:
		user, err = t.gitDevice(ctx, st, svc)
	case gitforge.MethodCode:
		user, err = t.gitCode(ctx, st, svc)
	default:
		user, err = t.gitToken(ctx, st, svc)
	}
	switch {
	case err == errGitCancelled:
		t.addMessage(AuthorSystem, t.tr("git connect cancelled: nothing was stored."))
	case err != nil:
		msg := t.trf("could not connect %s: %v", svc.Name, err)
		if method != gitforge.MethodToken {
			msg += "\n" + t.trf("You can connect with a token instead: /git connect %s token", svc.ID)
		}
		t.addMessage(AuthorSystem, msg)
	default:
		t.addMessage(AuthorSystem, t.trf("%s: connected as %s.", svc.Name, user))
	}
}

// errGitCancelled marks a login the user abandoned.
var errGitCancelled = fmt.Errorf("cancelled")

// gitPick shows the numbered list of hosts and reads the choice.
func (t *TUI) gitPick(ctx context.Context) (gitforge.Service, bool) {
	accounts := t.forge().Accounts()
	var b strings.Builder
	b.WriteString(t.tr("Connect to which git host?") + "\n")
	for i, a := range accounts {
		fmt.Fprintf(&b, "  %d) %s\n", i+1, a.Service.Name)
	}
	b.WriteString(t.tr("Type a number or a name (gitlab:git.example.com for a self-hosted one); Esc cancels."))
	t.addPreformatted(AuthorSystem, b.String())
	for {
		line, ok := t.gitLineIn(ctx, false)
		if !ok || line == "" {
			t.addMessage(AuthorSystem, t.tr("git connect cancelled: nothing was stored."))
			return gitforge.Service{}, false
		}
		if i, err := strconv.Atoi(line); err == nil && i >= 1 && i <= len(accounts) {
			return accounts[i-1].Service, true
		}
		svc, err := t.gitHost(line)
		if err == nil {
			return svc, true
		}
		t.addMessage(AuthorSystem, err.Error())
	}
}

// gitDevice runs the device grant: show the URL and the code, open the browser, wait.
func (t *TUI) gitDevice(ctx context.Context, st gitforge.Store, svc gitforge.Service) (string, error) {
	flow, err := st.StartDevice(ctx, svc)
	if err != nil {
		return "", err
	}
	t.addPreformatted(AuthorSystem, t.trf("To connect %s:\n  1. Open  %s\n  2. Enter the code  %s\nWaiting for you to approve it in the browser... (Esc cancels)", svc.Name, flow.URL, flow.Code))
	t.openBrowser(flow.URL)
	return t.gitAwait(ctx, func(c context.Context, _ <-chan string) (string, error) { return flow.Wait(c) })
}

// gitCode runs the authorisation-code grant, which also accepts a pasted code or URL.
func (t *TUI) gitCode(ctx context.Context, st gitforge.Store, svc gitforge.Service) (string, error) {
	flow, err := st.StartCode(svc)
	if err != nil {
		return "", err
	}
	t.addPreformatted(AuthorSystem, t.trf("To connect %s, open this address and approve:\n  %s\nWaiting for the browser... If it runs on another machine, paste here the address it ends on. (Esc cancels)", svc.Name, flow.URL))
	t.openBrowser(flow.URL)
	user, err := t.gitAwait(ctx, flow.Wait)
	if err == errGitCancelled {
		flow.Cancel()
	}
	return user, err
}

// gitToken connects with a token typed in without echo.
func (t *TUI) gitToken(ctx context.Context, st gitforge.Store, svc gitforge.Service) (string, error) {
	t.addPreformatted(AuthorSystem, t.trf("Create a token for motita at:\n  %s", svc.TokenURL))
	t.openBrowser(svc.TokenURL)
	username := ""
	if svc.Kind == gitforge.KindBitbucket {
		t.addMessage(AuthorSystem, t.tr("Your Bitbucket account name:"))
		var ok bool
		if username, ok = t.gitLineIn(ctx, false); !ok {
			return "", errGitCancelled
		}
	}
	t.addMessage(AuthorSystem, t.tr("Paste the token (it is not shown); Esc cancels:"))
	token, ok := t.gitLineIn(ctx, true)
	if !ok {
		return "", errGitCancelled
	}
	return st.ConnectToken(ctx, svc, token, username)
}

// gitResult is what a login's wait produced.
type gitResult struct {
	user string
	err  error
}

// gitAwait waits for a login while reading the keyboard: a pasted line goes to the flow, Esc (or
// a closed input) abandons it. The messages are written before and after, never while the reader
// runs, so the reader is the only thing painting.
func (t *TUI) gitAwait(ctx context.Context, wait func(context.Context, <-chan string) (string, error)) (string, error) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	paste := make(chan string, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			line, ok := t.gitLineIn(wctx, false)
			if !ok {
				return
			}
			select {
			case paste <- line:
			default:
			}
		}
	}()
	done := make(chan gitResult, 1)
	go func() {
		user, err := wait(wctx, paste)
		done <- gitResult{user, err}
	}()
	t.beginTurn()
	var res gitResult
	select {
	case res = <-done:
		cancel()
		<-readerDone
	case <-readerDone:
		cancel()
		<-done
		res = gitResult{err: errGitCancelled}
	}
	t.endTurn()
	if ctx.Err() != nil {
		return "", errGitCancelled
	}
	return res.user, res.err
}

// gitLineIn reads one line from the user inside a git flow. ok is false when the flow is
// abandoned: Esc, a cancelled context, or a closed input.
func (t *TUI) gitLineIn(ctx context.Context, secret bool) (string, bool) {
	if t.gitLine != nil {
		return t.gitLine(ctx, secret)
	}
	t.draw.Lock()
	t.prompting, t.maskInput = true, secret
	t.draw.Unlock()
	defer func() {
		t.draw.Lock()
		t.prompting, t.maskInput = false, false
		t.draw.Unlock()
	}()
	for {
		line, ok := t.readLine(ctx)
		if !ok || line == keyEsc {
			return "", false
		}
		// A key (arrow, Tab, control byte) is not an answer.
		if line != "" && line[0] < 0x20 {
			continue
		}
		return strings.TrimSpace(line), true
	}
}

// openBrowser makes a best effort to open url; the address is always printed too, so a failure
// is silent.
func (t *TUI) openBrowser(url string) {
	open := t.openURL
	if open == nil {
		open = openInBrowser
	}
	_ = open(url)
}

// startProcess starts a command without waiting for it; a variable so a test can stand in.
var startProcess = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

// openCommand is the command that opens a URL on an operating system.
func openCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
	return "xdg-open", []string{url}
}

// openInBrowser opens url in the user's browser. A machine without a display has no browser to
// open and the user follows the printed address.
func openInBrowser(url string) error {
	if os.Getenv("MOTITA_NO_BROWSER") != "" {
		return nil
	}
	name, args := openCommand(runtime.GOOS, url)
	return startProcess(name, args...)
}

// shownDraft is the draft as it is drawn: bullets while a secret is typed.
func (t *TUI) shownDraft() string {
	if t.maskInput {
		return strings.Repeat("•", len([]rune(t.draft)))
	}
	return t.draft
}
