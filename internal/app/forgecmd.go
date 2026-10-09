package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/semantic"
)

// forgeCommand is the word that starts `motita forge ...`: the commands the agent
// runs (from its shell) to open a pull request and to read its CI, and the user
// can run too. They exist so that the agent never needs a token in its hands: the
// connection the user made in the settings is used from here.
const forgeCommand = "forge"

// forgeCIPending is the exit status of "pr checks" when the CI has not ended or does
// not exist. It is not 2, which is ConfigError: a script must tell "wrong flags" from
// "not finished".
const forgeCIPending = 3

const forgeHelp = `Usage: motita forge <command>

Commands:
  status                              the git hosts that are connected
  pr create --title T [--body B]      open a pull request for the current branch
  pr status [N]                       the pull request of this branch (or number N) and its CI
  pr checks [N] [--wait] [--logs]     the CI of the pull request; --wait blocks until it ends

pr create flags:
  --title T        required, in the form "type(scope): description" (Conventional Commits)
  --body B         the description; --body-file F reads it from a file ("-" is stdin)
  --base BRANCH    the branch to merge into (default: the repository's default branch)
  --head BRANCH    the branch with the work (default: the current branch)
  --draft          open it as a draft

pr checks flags:
  --wait           wait while the CI is running
  --timeout D      how long --wait waits (default 30m)
  --logs           print the end of the log of each failed job (GitHub)

Run it inside the repository. Exit status of "pr checks": 0 the CI passed, 1 it failed,
3 it is still running or there is no CI.
`

// Seams of the forge commands: git, the network and the clock are the three
// things a test must be able to replace.
var (
	// forgeGit runs git in dir and returns its trimmed output.
	forgeGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append(append([]string{"-C", dir}, gitx.Hardening()...), args...)...)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	// forgeHTTP is the transport of the host's API; nil is the package default.
	forgeHTTP oauth.HTTPClient
	// forgeSleep waits between polls of the CI.
	forgeSleep = time.After
	// forgePollEvery is how often --wait asks the host.
	forgePollEvery = 20 * time.Second
)

// forgeStore is the store the commands read the logins from.
func forgeStore() gitforge.Store {
	return gitforge.Store{Dir: config.AuthDir(), HTTP: forgeHTTP}
}

// runGitCredential is `motita git-credential get|store|erase`: git's side of the
// credential protocol, answered from the logins motita holds.
func (op Options) runGitCredential(ctx context.Context, args []string) int {
	action := ""
	if len(args) > 1 {
		action = args[1]
	}
	forgeStore().RunHelper(ctx, action, op.Stdin, op.Out)
	return Success
}

// runForge dispatches `motita forge <command>`.
func (op Options) runForge(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(op.Err, forgeHelp)
		return ConfigError
	}
	switch args[0] {
	case "status":
		return op.forgeStatus()
	case "pr":
		if len(args) < 2 {
			fmt.Fprint(op.Err, forgeHelp)
			return ConfigError
		}
		switch args[1] {
		case "create":
			return op.forgePRCreate(ctx, args[2:])
		case "status":
			return op.forgePRChecks(ctx, args[2:], false)
		case "checks":
			return op.forgePRChecks(ctx, args[2:], true)
		}
	case "help", "-h", "--help":
		fmt.Fprint(op.Out, forgeHelp)
		return Success
	}
	fmt.Fprintf(op.Err, "unknown forge command %q\n\n%s", strings.Join(args, " "), forgeHelp)
	return ConfigError
}

func (op Options) forgeStatus() int {
	listed := false
	for _, a := range forgeStore().Accounts() {
		if !a.Connected {
			continue
		}
		listed = true
		fmt.Fprintf(op.Out, "%s: connected as %s\n", a.Service.Name, a.Username)
	}
	if !listed {
		fmt.Fprintln(op.Out, "no git host is connected: connect one in the motita settings (or with /git in the terminal interface)")
	}
	return Success
}

// forgeRepo is the repository a command is about: the service its origin belongs
// to, the repository on it, and the branch checked out.
type forgeRepo struct {
	dir    string
	store  gitforge.Store
	svc    gitforge.Service
	remote gitforge.Remote
	branch string
}

// openRepo resolves the repository in the working directory, with an error that
// says what the user can do about it.
func (op Options) openRepo(ctx context.Context) (forgeRepo, gitforge.API, error) {
	dir, _ := os.Getwd()
	url, err := forgeGit(ctx, dir, "remote", "get-url", "origin")
	if err != nil || url == "" {
		return forgeRepo{}, gitforge.API{}, errors.New("this repository has no remote named origin: add one with `git remote add origin <url>`")
	}
	store := forgeStore()
	svc, remote, err := store.ForRemote(url)
	switch {
	case errors.Is(err, gitforge.ErrNoService):
		return forgeRepo{}, gitforge.API{}, fmt.Errorf("%s is not a git host motita knows: it handles GitHub, GitLab, Bitbucket and Gitea", remote.Host)
	case err != nil:
		return forgeRepo{}, gitforge.API{}, err
	}
	api, err := store.Open(ctx, svc)
	if gitforge.IsNotConnected(err) {
		return forgeRepo{}, gitforge.API{}, fmt.Errorf("%s is not connected: connect it in the motita settings (or with /git in the terminal interface) and try again", svc.Name)
	}
	if err != nil {
		return forgeRepo{}, gitforge.API{}, err
	}
	branch, _ := forgeGit(ctx, dir, "branch", "--show-current")
	return forgeRepo{dir: dir, store: store, svc: svc, remote: remote, branch: branch}, api, nil
}

// defaultBranch is the branch the repository's origin treats as its default.
func defaultBranch(ctx context.Context, dir string) string {
	ref, err := forgeGit(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || !strings.HasPrefix(ref, "origin/") {
		return "main"
	}
	return strings.TrimPrefix(ref, "origin/")
}

func (op Options) forgePRCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("forge pr create", flag.ContinueOnError)
	fs.SetOutput(op.Err)
	title := fs.String("title", "", "")
	body := fs.String("body", "", "")
	bodyFile := fs.String("body-file", "", "")
	base := fs.String("base", "", "")
	head := fs.String("head", "", "")
	draft := fs.Bool("draft", false, "")
	if err := fs.Parse(args); err != nil {
		return ConfigError
	}
	// The title is checked before anything else, and before the network: a title
	// that is not semantic is refused with how to write one, whatever else is wrong.
	if err := semantic.LintSubject(*title); err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}
	text := *body
	if *bodyFile != "" {
		var data []byte
		var err error
		if *bodyFile == "-" {
			data, err = io.ReadAll(op.Stdin)
		} else {
			data, err = os.ReadFile(*bodyFile)
		}
		if err != nil {
			fmt.Fprintf(op.Err, "❌ could not read the description: %v\n", err)
			return ConfigError
		}
		text = string(data)
	}
	repo, api, err := op.openRepo(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	if *head == "" {
		*head = repo.branch
	}
	if *head == "" {
		fmt.Fprintln(op.Err, "❌ no branch is checked out: switch to the branch with the work, or pass --head")
		return ConfigError
	}
	if *base == "" {
		*base = defaultBranch(ctx, repo.dir)
	}
	if *head == *base {
		fmt.Fprintf(op.Err, "❌ the work is on %s, the branch it would be merged into: make a branch for it first\n", *base)
		return ConfigError
	}
	// A branch that already has an open pull request gets that one back instead of
	// a second: opening it twice is an error on the host and a duplicate on the others.
	if existing, ok, err := api.FindPR(ctx, repo.remote, *head); err == nil && ok {
		op.printPR(repo, existing, "is already open")
		return Success
	}
	pr, err := api.CreatePR(ctx, repo.remote, gitforge.PROptions{Title: *title, Body: text, Head: *head, Base: *base, Draft: *draft})
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", describePRError(err, *head))
		return RunError
	}
	op.printPR(repo, pr, "opened")
	return Success
}

// describePRError adds what the user can do to the refusals people actually hit.
func describePRError(err error, head string) error {
	var he *gitforge.HTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case 401:
			return errors.New("the host refused the login: connect again in the motita settings")
		case 403, 404:
			return fmt.Errorf("%w (the login may lack permission to open pull requests on this repository)", err)
		case 422:
			return fmt.Errorf("%w (is the branch %s pushed? run `git push -u origin %s` first)", err, head, head)
		}
	}
	return err
}

// printPR writes the pull request the way it must be repeated to the user: the
// number WITH its link, so it can be followed.
func (op Options) printPR(repo forgeRepo, pr gitforge.PullRequest, what string) {
	fmt.Fprintf(op.Out, "Pull request #%d %s: %s\n", pr.Number, what, pr.URL)
	fmt.Fprintf(op.Out, "Cite it as: [%s#%d](%s)\n", repo.remote.Path, pr.Number, pr.URL)
}

// parseInterspersed parses flags that may come before or after the positional
// arguments: `pr checks 12 --wait` is how it is written, and the flag package alone
// would stop at the 12 and leave --wait unread.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// forgePRChecks is `pr status` and `pr checks`: the same report, and the second
// can wait for the CI to end.
func (op Options) forgePRChecks(ctx context.Context, args []string, checks bool) int {
	fs := flag.NewFlagSet("forge pr checks", flag.ContinueOnError)
	fs.SetOutput(op.Err)
	wait := fs.Bool("wait", false, "")
	logs := fs.Bool("logs", false, "")
	timeout := fs.Duration("timeout", 30*time.Minute, "")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return ConfigError
	}
	repo, api, err := op.openRepo(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	var pr gitforge.PullRequest
	if len(positional) > 0 {
		n := positional[0]
		if _, err := fmt.Sscanf(n, "%d", &pr.Number); err != nil || pr.Number <= 0 {
			fmt.Fprintf(op.Err, "❌ %q is not a pull request number\n", n)
			return ConfigError
		}
	} else {
		found, ok, err := api.FindPR(ctx, repo.remote, repo.branch)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		if !ok {
			fmt.Fprintf(op.Err, "❌ the branch %q has no open pull request: open one with `motita forge pr create`\n", repo.branch)
			return RunError
		}
		pr = found
	}
	deadline := time.Now().Add(*timeout)
	for {
		st, err := api.CI(ctx, repo.remote, pr.Number)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		if !*wait || st.State != gitforge.StatePending || !time.Now().Before(deadline) {
			return op.printCI(ctx, repo, api, pr, st, checks && *logs)
		}
		select {
		case <-ctx.Done():
			return RunError
		case <-forgeSleep(forgePollEvery):
		}
	}
}

// printCI reports the checks and maps the state to the exit status.
func (op Options) printCI(ctx context.Context, repo forgeRepo, api gitforge.API, pr gitforge.PullRequest, st gitforge.CIStatus, logs bool) int {
	fmt.Fprintf(op.Out, "%s#%d CI: %s\n", repo.remote.Path, pr.Number, st.State)
	for _, c := range st.Checks {
		line := fmt.Sprintf("  %-9s %s", c.State, c.Name)
		if c.Detail != "" {
			line += " - " + c.Detail
		}
		if c.URL != "" && c.State == gitforge.StateFailure {
			line += " (" + c.URL + ")"
		}
		fmt.Fprintln(op.Out, line)
	}
	if logs {
		for _, c := range st.Checks {
			if c.State != gitforge.StateFailure {
				continue
			}
			if text, ok, err := api.FailureLog(ctx, repo.remote, c); err == nil && ok {
				fmt.Fprintf(op.Out, "\n--- end of the log of %s ---\n%s\n", c.Name, text)
			}
		}
	}
	switch st.State {
	case gitforge.StateSuccess:
		return Success
	case gitforge.StateFailure:
		return RunError
	default:
		return forgeCIPending
	}
}
