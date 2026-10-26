package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/schedule"
	"github.com/madkoding/motita/internal/semantic"
	"github.com/madkoding/motita/internal/webpush"
)

// THE AUTOPILOT: upkeep Motita does by itself, in two routines the person turns on from Settings.
//
// The weekly audit runs a read-only plan in each project and writes what is new to the notes. The
// draft pull requests turn ONE small, safe improvement per project into a pull request in draft.
// The agent never publishes anything in either: it runs unattended, so every push, pull request and
// merge it tries is refused for lack of someone to ask. What reaches the git host is done HERE, in
// Go, where the rules cannot be talked out of:
//
//   - only a branch named autopilotBranchPrefix+<session> is ever pushed, and never the base;
//   - the push is a plain push of that one branch (no force, no other ref);
//   - the pull request is always a draft, and nothing here calls the merge endpoint;
//   - a change over the size limits, or touching a path on the deny list, is not published;
//   - no pull request is opened while the open ones of its own are at the person's limit.
//
// Both routines are records of the schedule store with a fixed id, so the cadence, the last run and
// its outcome are the scheduled tasks' own. The config's switches stay the truth: a firing re-reads
// them, so a record that was left enabled does nothing once its switch is off.

// The ids of the two routines' records in the schedule store.
const (
	autopilotAuditID = "autopilot-audit"
	autopilotFixesID = "autopilot-fixes"
)

const (
	// autopilotEvery is how often both routines run.
	autopilotEvery = 7 * 24 * time.Hour
	// autopilotBranchPrefix starts the name of every branch the autopilot pushes. It is what the
	// limit counts, so a person's own branches never use up the allowance.
	autopilotBranchPrefix = "motita/auto-"
	// autopilotAuditTitle is the title of the session that holds a project's audits. One session per
	// project is reused every week, so the agent remembers what it already reported.
	autopilotAuditTitle = "Weekly audit"
	// autopilotFixTitle is the title of the session of a draft pull request.
	autopilotFixTitle = "Draft pull request"
	// autopilotNotesFolder is the folder of the vault the audit reports are written in.
	autopilotNotesFolder = "Weekly audits"
	// The size a change may have to be published as a draft: beyond it, a person should look first.
	autopilotMaxFiles = 8
	autopilotMaxLines = 200
	// autopilotReportBytes bounds the earlier report quoted to the agent.
	autopilotReportBytes = 6000
)

// autopilotDenied are the files the autopilot never publishes a change to: the pipeline that
// decides what is trusted, and the files that decide what is installed.
var autopilotDenied = []string{
	".github/", ".gitlab-ci", ".circleci/", ".githooks/", "dockerfile", "go.mod", "go.sum", "package.json",
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.toml", "cargo.lock", "gemfile.lock",
	"requirements.txt", "poetry.lock", ".env",
}

var (
	newFindings = regexp.MustCompile(`(?im)^\s*NEW FINDINGS:\s*(\d+)`)
	// noFix is the agent's answer when nothing meets the rules.
	noFix = regexp.MustCompile(`(?im)^\s*NOTHING TO FIX\b`)
)

// errAutopilotBusy is why a routine will not start: it already runs.
var errAutopilotBusy = errors.New("this routine is already running")

// pushBranchFn publishes one branch. It is a variable so a test can publish to a repository on disk
// instead of a host; the real one is gitPushBranch.
var pushBranchFn = gitPushBranch

// autopilotIDs are the routines, in the order the settings page lists them.
var autopilotIDs = []string{autopilotAuditID, autopilotFixesID}

// isAutopilotID says a schedule record belongs to the autopilot.
func isAutopilotID(id string) bool { return id == autopilotAuditID || id == autopilotFixesID }

// autopilotTitle is the title the record of a routine carries, which the scheduled tasks list shows.
func autopilotTitle(id string) string {
	if id == autopilotAuditID {
		return autopilotAuditTitle
	}
	return "Draft pull requests"
}

// autopilotBegin marks a routine as running, or says it already is.
func (s *Server) autopilotBegin(id string) bool {
	s.autopilotMu.Lock()
	defer s.autopilotMu.Unlock()
	if s.autopilotBusy[id] {
		return false
	}
	if s.autopilotBusy == nil {
		s.autopilotBusy = map[string]bool{}
	}
	s.autopilotBusy[id] = true
	return true
}

func (s *Server) autopilotEnd(id string) {
	s.autopilotMu.Lock()
	defer s.autopilotMu.Unlock()
	delete(s.autopilotBusy, id)
}

func (s *Server) autopilotRunning(id string) bool {
	s.autopilotMu.Lock()
	defer s.autopilotMu.Unlock()
	return s.autopilotBusy[id]
}

// runAutopilot is one firing of a routine by the clock. The outcome is the sentence the record
// keeps; an error is a firing that did not happen.
func (s *Server) runAutopilot(ctx context.Context, id string) (string, error) {
	if !s.autopilotBegin(id) {
		return "", errAutopilotBusy
	}
	defer s.autopilotEnd(id)
	cfg, _, err := s.securityConfig()
	if err != nil {
		return "", fmt.Errorf("the configuration could not be read: %w", err)
	}
	return s.runAutopilotLocked(ctx, id, cfg), nil
}

// runAutopilotLocked is the firing itself, for a caller that already holds the routine's running
// mark. It re-reads nothing: cfg is the configuration the firing was decided with, and a routine
// whose switch is off does nothing.
func (s *Server) runAutopilotLocked(ctx context.Context, id string, cfg config.Config) string {
	if id == autopilotAuditID {
		if !cfg.Autopilot.WeeklyAudit {
			return "the weekly audit is switched off in Settings"
		}
		return s.auditProjects(ctx, cfg)
	}
	if !cfg.Autopilot.AutoPR {
		return "draft pull requests are switched off in Settings"
	}
	return s.fixProjects(ctx, cfg)
}

// languageOf is the language the person reads, for the reports the agent writes.
func languageOf(cfg config.Config) string {
	if i18n.Resolve(cfg.UI.Language, os.Getenv) == i18n.ES {
		return "Spanish"
	}
	return "English"
}

// auditTask is what the agent is told in a project's audit session.
func auditTask(language string) string {
	return `Run the weekly audit of this project. It is READ-ONLY: change nothing, run no exploit, print no secret.

Follow the auditing-code and finding-vulnerabilities skills, and verify every claim as verifying-a-change ` +
		`prescribes: a finding needs the evidence of a second read, not a hunch.

This conversation holds the audits of earlier weeks. Report ONLY what is new since then (new code, or ` +
		`something earlier audits missed), and list in one line each the earlier findings that are now fixed. ` +
		`Do not repeat a finding already reported. When there is an earlier audit, start from what changed: ` +
		`"git log" and the diff since then.

Write the report in ` + language + `, in plain words, without jargon. For each new finding give: a short ` +
		`title, the severity (high, medium or low), where it is (file:line), why it matters in one or two ` +
		`sentences, how to fix it, and whether the fix is small and safe (a text, a missing test, a lint or ` +
		`Sonar warning) or needs a person to decide.

End the report with one last line, exactly in this form: NEW FINDINGS: <number>`
}

// fixTask is what the agent is told in the worktree of a draft pull request.
func fixTask(branch, earlier string) string {
	known := ""
	if earlier != "" {
		known = "\nThe latest weekly audit of this project said:\n\n" + earlier + "\n"
	}
	return fmt.Sprintf(`Make ONE small, safe improvement to this project and commit it. Nothing else.

Pick one thing, in this order: a finding of the latest weekly audit that it marks as small and safe; a Sonar `+
		`warning, when the project has a Sonar report you can read without credentials; a missing test for `+
		`code that already exists; a typo or unclear wording in a text.
%[1]s
The rules, all of them:
- The change is small: at most %[2]d files and %[3]d changed lines in total.
- Never touch workflows or CI files (.github/ and the like), dependency files or lockfiles, Dockerfiles, `+
		`secrets, nor code that handles security (authentication, sandboxing, permissions).
- Follow the project's own rules (its contributing guide, tests, formatting). Run its checks before you `+
		`commit, and commit only if they pass. When you change behaviour, add or fix a test.
- Commit with a semantic message ("type(scope): subject" with feat, fix, docs, test, refactor or chore), `+
		`and explain why in the body. You are on the branch %[4]s: stay on it.
- Do not push, do not open a pull request and do not merge. Motita does that after you finish, as a draft `+
		`for the person to review.
- If nothing meets the rules, change nothing and answer exactly: NOTHING TO FIX`,
		known, autopilotMaxFiles, autopilotMaxLines, branch)
}

// awaitRun runs a task unattended in a session and waits for it. It answers the run's result, or why
// there is none.
func (s *Server) awaitRun(ctx context.Context, c *conversation, task, kind, routine string) (string, error) {
	rn, ok := s.startDetachedRun(c, task, kind, "", s.unattendedApprover(routine))
	if !ok {
		return "", errors.New("its session is busy")
	}
	select {
	case <-rn.done:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	outcome, result, errText, _ := rn.outcomeOf()
	if outcome != "done" {
		return "", fmt.Errorf("the run did not finish (%s) %s", outcome, firstLine(errText))
	}
	return result, nil
}

// notifyAutopilot pushes a notification to the phones, in each one's language. text is the English
// body; a tap opens the app.
func (s *Server) notifyAutopilot(tag, head, body string, args ...any) {
	if s.push == nil {
		return
	}
	s.pushWG.Go(func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, pushTimeout)
		defer cancel()
		if _, err := s.push.Send(ctx, func(lang string) webpush.Message {
			return webpush.Message{Title: i18n.T(i18n.Lang(lang), head), Body: i18n.Tf(i18n.Lang(lang), body, args...),
				Tag: tag, URL: "/"}
		}); err != nil {
			s.warn("could not send a phone notification", "tag", tag, "error", err.Error())
		}
	})
}

// auditProjects audits every project, one after the other, and says how it went.
func (s *Server) auditProjects(ctx context.Context, cfg config.Config) string {
	projects := s.allProjects()
	if len(projects) == 0 {
		return "there are no projects to audit"
	}
	var total, audited int
	var problems []string
	for _, p := range projects {
		if ctx.Err() != nil {
			break
		}
		n, err := s.auditProject(ctx, cfg, p)
		if err != nil {
			problems = append(problems, p.Title+": "+err.Error())
			continue
		}
		audited++
		if n > 0 {
			total += n
		}
	}
	out := fmt.Sprintf("audited %d of %d projects: %d new findings", audited, len(projects), total)
	if len(problems) > 0 {
		out += "; " + strings.Join(problems, "; ")
	}
	return out
}

// auditSessionOf is the session that holds a project's audits, made the first time.
func (s *Server) auditSessionOf(p Project) (*conversation, error) {
	for _, c := range s.snapshot() {
		c.stateMu.Lock()
		mine := c.projectID == p.ID && c.title == autopilotAuditTitle
		c.stateMu.Unlock()
		if mine {
			return c, nil
		}
	}
	c, err := s.createSession()
	if err != nil {
		return nil, err
	}
	s.placeAndSave(c, p.ID, &p, nil, false)
	c.setTitle(autopilotAuditTitle)
	s.saveSession(c)
	return c, nil
}

// auditProject runs one project's audit and writes its report. It answers how many findings are
// new, or -1 when the report did not say.
func (s *Server) auditProject(ctx context.Context, cfg config.Config, p Project) (int, error) {
	c, err := s.auditSessionOf(p)
	if err != nil {
		return 0, err
	}
	result, err := s.awaitRun(ctx, c, auditTask(languageOf(cfg)), schedule.KindPlan, autopilotAuditID)
	if err != nil {
		return 0, err
	}
	n := -1
	if m := newFindings.FindStringSubmatch(result); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	s.writeAuditNote(p, result)
	if n != 0 {
		s.notifyAutopilot("motita-audit-"+p.ID, autopilotAuditTitle,
			"The weekly audit of %s has a new report: look at the notes.", p.Title)
	}
	return n, nil
}

// auditNoteFile is the vault path of the report of a project's audit of day, and auditNotePrefix the
// folder holding all of that project's reports.
func auditNoteFile(p Project, day time.Time) string {
	return notes.FileName(autopilotNotesFolder, p.Title+"/"+day.Format("2006-01-02"))
}

func auditNotePrefix(p Project) string {
	return strings.TrimSuffix(notes.FileName(autopilotNotesFolder, p.Title+"/x"), "x.md")
}

// writeAuditNote puts a report in the notes, when there is a vault. A failure is a warning: the
// report is still in the audit session.
func (s *Server) writeAuditNote(p Project, report string) {
	if s.opts.NotesDir == "" {
		return
	}
	body := "# " + p.Title + "\n\n" + strings.TrimSpace(report) + "\n"
	if _, err := notes.Open(s.opts.NotesDir).Write(auditNoteFile(p, time.Now()), body, false); err != nil {
		s.warn("could not write the weekly audit note", "project", p.ID, "error", err.Error())
	}
}

// latestAudit is the newest audit report of a project, cut to a size the agent can be shown.
func (s *Server) latestAudit(p Project) string {
	if s.opts.NotesDir == "" {
		return ""
	}
	v := notes.Open(s.opts.NotesDir)
	prefix := auditNotePrefix(p)
	var mine []string
	for _, n := range v.List() {
		if strings.HasPrefix(n.Path, prefix) {
			mine = append(mine, n.Path)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	sort.Strings(mine)
	body, _, _ := v.Read(mine[len(mine)-1]) // a report that cannot be read is no report: body is empty
	if len(body) > autopilotReportBytes {
		body = body[:autopilotReportBytes] + "\n…"
	}
	return body
}

// forgeTarget is a project's repository on its git host.
type forgeTarget struct {
	api    gitforge.API
	remote gitforge.Remote
}

// key tells two projects of one repository apart from two repositories.
func (t forgeTarget) key() string { return t.api.Service.ID + ":" + t.remote.Path }

// forgeOf resolves a project's repository on its host, or says why it has none.
func (s *Server) forgeOf(ctx context.Context, p Project) (forgeTarget, error) {
	return s.forgeOfRepo(ctx, p.Dir, p.GitURL)
}

// forgeOfRepo resolves the repository in dir on its host: gitURL when the project recorded one,
// else the origin of the checkout.
func (s *Server) forgeOfRepo(ctx context.Context, dir, gitURL string) (forgeTarget, error) {
	st := s.gitStore()
	if st == nil {
		return forgeTarget{}, errors.New("this gateway has no directory to keep git logins in")
	}
	if gitx.Repo(ctx, dir) != nil {
		return forgeTarget{}, errors.New("it is not a git repository")
	}
	url := strings.TrimSpace(gitURL)
	if url == "" {
		url = s.originOf(ctx, dir)
	}
	if url == "" {
		return forgeTarget{}, errors.New("it has no remote named origin")
	}
	svc, remote, err := st.ForRemote(url)
	if err != nil {
		return forgeTarget{}, fmt.Errorf("%s is not a git host Motita can open pull requests on", remote.Host)
	}
	api, err := st.Open(ctx, svc)
	if err != nil {
		return forgeTarget{}, fmt.Errorf("%s is not connected", svc.Name)
	}
	api.Cache = &s.etags
	return forgeTarget{api: api, remote: remote}, nil
}

// fixProjects opens at most one draft pull request per project, while fewer than the limit of the
// autopilot's own are open.
func (s *Server) fixProjects(ctx context.Context, cfg config.Config) string {
	projects := s.allProjects()
	if len(projects) == 0 {
		return "there are no projects"
	}
	targets := map[string]forgeTarget{}
	var problems []string
	open := 0
	counted := map[string]bool{}
	for _, p := range projects {
		t, err := s.forgeOf(ctx, p)
		if err != nil {
			problems = append(problems, p.Title+": "+err.Error())
			continue
		}
		targets[p.ID] = t
		if counted[t.key()] {
			continue
		}
		counted[t.key()] = true
		prs, err := t.api.OpenPRsFrom(ctx, t.remote, autopilotBranchPrefix)
		if err != nil {
			delete(targets, p.ID)
			problems = append(problems, p.Title+": "+err.Error())
			continue
		}
		open += len(prs)
	}
	limit := cfg.Autopilot.MaxOpenPRs
	var opened []string
	for _, p := range projects {
		t, ok := targets[p.ID]
		if !ok || ctx.Err() != nil {
			continue
		}
		if open >= limit {
			problems = append(problems, fmt.Sprintf("limit of %d open draft pull requests reached", limit))
			break
		}
		url, err := s.fixProject(ctx, p, t)
		switch {
		case err != nil:
			problems = append(problems, p.Title+": "+err.Error())
		case url != "":
			open++
			opened = append(opened, url)
		default:
			// The agent found nothing to fix: no pull request, and no problem to report.
		}
	}
	out := fmt.Sprintf("opened %d draft pull requests", len(opened))
	if len(opened) > 0 {
		out += ": " + strings.Join(opened, ", ")
	}
	if len(problems) > 0 {
		out += "; " + strings.Join(problems, "; ")
	}
	return out
}

// fixProject lets the agent make one small change in a worktree of its own and publishes it as a
// draft pull request. It answers the link, or "" when there was nothing to publish.
func (s *Server) fixProject(ctx context.Context, p Project, t forgeTarget) (string, error) {
	c, err := s.createSession()
	if err != nil {
		return "", err
	}
	s.placeAndSave(c, p.ID, &p, nil, true)
	// A session that could not get a worktree of its own would edit the project's checkout: the
	// autopilot never works there.
	if gitx.SamePath(c.workspace, p.Dir) {
		return "", errors.New("it could not get a worktree of its own")
	}
	dir := c.sessionRepoDir(ctx, "")
	base := gitx.DefaultBranch(ctx, dir)
	branch := autopilotBranchPrefix + c.id
	// A branch that could not be made is caught by vetChange, which publishes only from the
	// branch the autopilot named.
	_ = gitx.SwitchNew(ctx, dir, branch)
	c.setTitle(autopilotFixTitle)
	start := gitx.HeadRev(ctx, dir)
	result, err := s.awaitRun(ctx, c, fixTask(branch, s.latestAudit(p)), schedule.KindTask, autopilotFixesID)
	if err != nil {
		return "", err
	}
	if noFix.MatchString(result) {
		return "", nil
	}
	title, body, err := s.vetChange(ctx, dir, base, branch, start)
	if err != nil {
		return "", err
	}
	if err := pushBranchFn(ctx, s.gitCommandEnv(), dir, branch); err != nil {
		return "", fmt.Errorf("the branch could not be pushed: %w", err)
	}
	pr, err := t.api.CreatePR(ctx, t.remote,
		gitforge.PROptions{Title: title, Body: body, Head: branch, Base: base, Draft: true})
	if err != nil {
		return "", fmt.Errorf("the pull request could not be opened: %w", err)
	}
	s.notifyAutopilot("motita-autopr-"+c.id, "Draft pull request opened",
		"Motita opened a draft pull request in %s: %s", p.Title, pr.URL)
	return pr.URL, nil
}

// vetChange decides whether the agent's work may be published, and says what the pull request is
// called and tells. It is the gate: the agent could have done anything in its worktree.
func (s *Server) vetChange(ctx context.Context, dir, base, branch, start string) (title, body string, err error) {
	if !strings.HasPrefix(branch, autopilotBranchPrefix) || branch == base {
		return "", "", errors.New("the work is not on a branch of its own")
	}
	if head, _, herr := gitx.Head(ctx, dir); herr != nil || head != branch {
		return "", "", errors.New("the work is not on the branch the autopilot made")
	}
	if n, _ := gitx.WorkingTreeChanges(ctx, dir); n > 0 {
		return "", "", errors.New("it left changes that are not committed, so nothing was published")
	}
	ahead, _, cerr := gitx.CommitsBetween(ctx, dir, base, branch)
	if cerr != nil || len(ahead) == 0 {
		return "", "", errors.New("it made no commit")
	}
	changes, cerr := gitx.ChangesSince(ctx, dir, start)
	if cerr != nil {
		return "", "", fmt.Errorf("its change could not be measured: %w", cerr)
	}
	lines := 0
	for _, ch := range changes {
		if deniedPath(ch.Path) {
			return "", "", fmt.Errorf("it touched %s, which the autopilot never changes", ch.Path)
		}
		lines += ch.Added + ch.Deleted
	}
	if len(changes) > autopilotMaxFiles || lines > autopilotMaxLines {
		return "", "", fmt.Errorf("the change is too big to publish on its own (%d files, %d lines)",
			len(changes), lines)
	}
	// ahead lists the newest first: the title is the subject of the last commit made.
	title = ahead[0].Subject
	if lerr := semantic.LintSubject(title); lerr != nil {
		return "", "", fmt.Errorf("its commit message is not semantic: %w", lerr)
	}
	var b strings.Builder
	b.WriteString("Opened by Motita's upkeep, as a draft: nothing here was merged or pushed to the main branch, ")
	b.WriteString("and Motita never merges it. Review it like any other pull request.\n\n## What\n\n")
	for i := len(ahead) - 1; i >= 0; i-- {
		b.WriteString("- " + ahead[i].Subject + "\n")
	}
	fmt.Fprintf(&b, "\n## How it was checked\n\nThe change is %d files and %d lines. ", len(changes), lines)
	b.WriteString("Motita ran the project's own checks ")
	b.WriteString("before committing; the CI of this pull request is the second look.\n")
	return title, b.String(), nil
}

// deniedPath says a path may not be part of a published change.
func deniedPath(p string) bool {
	low := strings.ToLower(path.Clean(strings.ReplaceAll(p, "\\", "/")))
	base := path.Base(low)
	for _, d := range autopilotDenied {
		if deniedBy(d, low, base) {
			return true
		}
	}
	return false
}

// deniedBy says one entry of the deny list names the path: a folder by its prefix, a file by the
// start of its name.
func deniedBy(entry, low, base string) bool {
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(low, entry) || strings.Contains(low, "/"+entry)
	}
	return strings.HasPrefix(base, entry)
}

// gitPushBranch pushes one branch to origin: that ref only, as a plain push.
func gitPushBranch(ctx context.Context, env []string, dir, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := append([]string{"-C", dir}, gitx.HardeningFor(ctx, dir)...)
	args = append(args, "push", "--no-verify", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(string(out)))
	}
	return nil
}
