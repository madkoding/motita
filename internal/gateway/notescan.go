package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// The scan fills the vault from what motita already holds: one note per project and one per
// session, each kept in a block the scan rewrites, so what the person adds around it stays.
const (
	scanProjectsFolder = "Projects"
	scanSessionsFolder = "Sessions"
	scanQuoteBytes     = 600
	scanReadmeBytes    = 3000
	scanLastTurns      = 3
	scanTimeFormat     = "2006-01-02 15:04"
	scanMarker         = ".scanned"
)

// scanMaxSessions is how many sessions, the most recent, the scan writes. A variable so a test can
// lower it.
var scanMaxSessions = 1000

// noteScan is what POST /v1/notes/scan answers.
type noteScan struct {
	// Ran is false when the scan was asked only for a vault that has never been filled, and it is not.
	Ran       bool `json:"ran"`
	Projects  int  `json:"projects"`
	Sessions  int  `json:"sessions"`
	Created   int  `json:"created"`
	Updated   int  `json:"updated"`
	Unchanged int  `json:"unchanged"`
	// Summarized counts the sessions the model summarised in this scan.
	Summarized int `json:"summarized"`
	// Mapped counts the projects whose architecture note was written; Documented, the notes of their
	// manuals (one per folder); Explained, the projects and modules the model explained in this scan.
	Mapped     int `json:"mapped"`
	Documented int `json:"documented"`
	Explained  int `json:"explained"`
	// Skipped counts sessions with nothing said yet, and notes the vault refused.
	Skipped int `json:"skipped"`
	// tracker hears how far a background scan got; nil for one answered in the request.
	tracker *scanTracker
}

func (n *noteScan) count(outcome string, err error) {
	switch {
	case err != nil:
		n.Skipped++
	case outcome == notes.Created:
		n.Created++
	case outcome == notes.Updated:
		n.Updated++
	default:
		n.Unchanged++
	}
}

// scanSession is what the scan reads of a conversation, taken under its lock.
type scanSession struct {
	id, title, projectID string
	created, lastUsed    time.Time
	turns                []agent.DialogueTurn
	svc                  Service
}

func (s *Server) scanSessions() []scanSession {
	var out []scanSession
	for _, c := range s.snapshot() {
		c.stateMu.Lock()
		ss := scanSession{id: c.id, title: c.title, projectID: c.projectID, created: c.created, lastUsed: c.lastUsed}
		c.stateMu.Unlock()
		if c.svc != nil {
			ss.svc = c.svc
			ss.turns = c.svc.Transcript()
		}
		out = append(out, ss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lastUsed.After(out[j].lastUsed) })
	if len(out) > scanMaxSessions {
		out = out[:scanMaxSessions]
	}
	return out
}

// quote turns a text into a Markdown quote that cannot open a link, close the scan block or leak
// a secret.
func quote(text string, max int) string {
	text = clip(redact.String(text), max)
	text = strings.NewReplacer(notes.BlockStart, "", notes.BlockEnd, "", "[[", "[ [").Replace(text)
	return "> " + strings.ReplaceAll(text, "\n", "\n> ")
}

// sessionNoteName is the vault path of a session: its title and the start of its id, which tells
// two sessions with the same title apart.
func sessionNoteName(title, id string) string {
	if strings.TrimSpace(title) == "" {
		title = "Session"
	}
	short := strings.TrimPrefix(id, "s")
	if len(short) > 8 {
		short = short[:8]
	}
	return notes.FileName(scanSessionsFolder, title+" ("+short+")")
}

func sessionBlock(ss scanSession, projectLink, summary string) string {
	var b strings.Builder
	b.WriteString("## Session\n")
	if projectLink != "" {
		fmt.Fprintf(&b, "- Project: %s\n", projectLink)
	}
	fmt.Fprintf(&b, "- Started: %s\n- Last used: %s\n- Turns: %d\n",
		ss.created.Format(scanTimeFormat), ss.lastUsed.Format(scanTimeFormat), len(ss.turns))
	b.WriteString(summarySection(summary, len(ss.turns)))
	b.WriteString("\n### First request\n" + quote(ss.turns[0].User, scanQuoteBytes) + "\n")
	from := max(0, len(ss.turns)-scanLastTurns)
	b.WriteString("\n### Latest exchanges\n")
	for _, t := range ss.turns[from:] {
		if strings.TrimSpace(t.User) != "" {
			b.WriteString("**Person**\n" + quote(t.User, scanQuoteBytes) + "\n\n")
		}
		if strings.TrimSpace(t.Agent) != "" {
			b.WriteString("**Motita**\n" + quote(t.Agent, scanQuoteBytes) + "\n\n")
		}
	}
	return b.String()
}

// readmeExcerpt is the start of a project's README, redacted, or "" when it has none.
func readmeExcerpt(dir string) string {
	for _, name := range []string{"README.md", "readme.md", "README"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return quote(string(raw), scanReadmeBytes)
		}
	}
	return ""
}

func projectBlock(p Project, links []string, archLink, guideLink string) string {
	var b strings.Builder
	b.WriteString("## About this project\n")
	if p.Description != "" {
		b.WriteString(quote(p.Description, scanQuoteBytes) + "\n\n")
	}
	if guideLink != "" {
		b.WriteString("- Read the guide: " + guideLink + "\n")
	}
	if archLink != "" {
		b.WriteString("- Technical overview: " + archLink + "\n")
	}
	if p.GitURL != "" {
		fmt.Fprintf(&b, "- Repository: %s\n", redact.String(p.GitURL))
	}
	if len(links) > 0 {
		b.WriteString("\n### Conversations\n")
		for _, l := range links {
			b.WriteString("- " + l + "\n")
		}
	}
	if readme := readmeExcerpt(p.Dir); readme != "" {
		b.WriteString("\n### README\n" + readme + "\n")
	}
	return b.String()
}

// wikiLink is how a note refers to another: its path without the extension, in double brackets.
func wikiLink(name string) string { return "[[" + strings.TrimSuffix(name, ".md") + "]]" }

// projectNoteNames gives every project the vault path of its note. Two projects can share a title:
// the second one is told apart by the start of its id.
func projectNoteNames(projects []Project) map[string]string {
	names := map[string]string{}
	taken := map[string]bool{}
	for _, p := range projects {
		name := notes.FileName(scanProjectsFolder, p.Title)
		if name == "" || taken[name] {
			name = notes.FileName(scanProjectsFolder, p.Title+" ("+p.ID[:min(8, len(p.ID))]+")")
		}
		taken[name] = true
		names[p.ID] = name
	}
	return names
}

// scanSessionNotes writes the note of every session that has something in it, counting what
// happened in res, and answers the links to each project's sessions.
func (s *Server) scanSessionNotes(
	ctx context.Context, v *notes.Vault, names map[string]string, sum *summaries, res *noteScan,
) map[string][]string {
	byProject := map[string][]string{}
	for _, ss := range s.scanSessions() {
		name := sessionNoteName(ss.title, ss.id)
		if len(ss.turns) == 0 || name == "" {
			res.Skipped++
			continue
		}
		res.Sessions++
		projectLink := ""
		if pn, ok := names[ss.projectID]; ok {
			projectLink = wikiLink(pn)
			byProject[ss.projectID] = append(byProject[ss.projectID], wikiLink(name))
		}
		// The heading is what the conversation was called; the id only tells the files apart.
		title := strings.TrimSpace(ss.title)
		if title == "" {
			title = "Session"
		}
		outcome, err := v.Upsert(name, title, sessionBlock(ss, projectLink, sum.of(ctx, v, name, ss)))
		res.count(outcome, err)
	}
	return byProject
}

// handleScanNotes writes a note for every project and every session that has been used, and
// answers what it did. Running it again updates the same notes. With ?background=1 it answers at
// once with the progress, which GET /v1/notes/scan follows.
func (s *Server) handleScanNotes(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	q := s.scanQuery(r)
	marker := filepath.Join(s.opts.NotesDir, scanMarker)
	if q.Get("first") != "" {
		// Only the first time: a vault that has notes, or was scanned before (and emptied since), is
		// the person's to fill.
		if _, err := os.Stat(marker); err == nil || len(v.List()) > 0 {
			writeJSON(w, http.StatusOK, noteScan{})
			return
		}
	}
	s.scanJob.remember(q)
	run := func(ctx context.Context, t *scanTracker) (noteScan, error) {
		return s.runScan(ctx, v, q, t)
	}
	if q.Get(scanBackground) != "" {
		s.startScan(w, run)
		return
	}
	res, err := run(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// scanBackground is the query flag that runs a scan in the background.
const scanBackground = "background"

// scanQuery is what a scan request asks for. "again" repeats the model options of the last scan, in
// the background: it is what the project menu's "Rescan the notes" sends, with its project and no
// options of its own to give.
func (s *Server) scanQuery(r *http.Request) url.Values {
	q := r.URL.Query()
	if q.Get("again") == "" {
		return q
	}
	// The project menu rescans its own project only.
	return s.againQuery(q.Get(scanProject))
}

// againQuery is a scan in the background with the model options of the last one, limited to the
// project id when there is one.
func (s *Server) againQuery(project string) url.Values {
	again := url.Values{scanBackground: {"1"}}
	for k, v := range s.lastScanOptions() {
		again[k] = v
	}
	if project != "" {
		again.Set(scanProject, project)
	}
	return again
}

// scanProject is the query parameter that limits a scan to one project; its sessions' notes are
// written all the same.
const scanProject = "project"

// scanOnly keeps the project a scan was limited to, or every project when it was not.
func scanOnly(projects []Project, id string) []Project {
	if id == "" {
		return projects
	}
	for _, p := range projects {
		if p.ID == id {
			return []Project{p}
		}
	}
	return nil
}

// runScan is the scan itself, reporting how far it got to t.
func (s *Server) runScan(ctx context.Context, v *notes.Vault, q url.Values, t *scanTracker) (noteScan, error) {
	var projects []Project
	if s.projects != nil {
		all, err := s.projects.loadAll()
		if err != nil {
			return noteScan{}, err
		}
		projects = all
	}
	names := projectNoteNames(projects)
	res := noteScan{Ran: true, tracker: t}
	sum := &summaries{on: q.Get(scanSummarize) != "", left: scanSummaryMax}
	byProject := s.scanSessionNotes(ctx, v, names, sum, &res)
	res.Summarized = sum.done
	ms := s.scanModelsFor(q.Get(scanSummarize) != "", explainBudget(q.Get(scanModules)))
	// Named from every project, so a note keeps its name when the scan is limited to one.
	projects = scanOnly(projects, q.Get(scanProject))
	for i, p := range projects {
		t.update(func(sp *scanProgress) {
			*sp = scanProgress{Running: true, Phase: "projects", Project: p.Title, Done: i, Total: len(projects)}
		})
		res.Projects++
		archLink, guideLink := scanArchitecture(ctx, v, p, names[p.ID], ms, &res)
		outcome, err := v.Upsert(names[p.ID], p.Title, projectBlock(p, byProject[p.ID], archLink, guideLink))
		res.count(outcome, err)
	}
	t.update(func(sp *scanProgress) { sp.Done = len(projects) })
	res.Explained = ms.done()
	// Remembered in the vault folder, as a hidden file notes never list, so any client sees it. It
	// keeps the model options too, which a rescan after a restart repeats.
	if os.MkdirAll(s.opts.NotesDir, 0o755) == nil {
		marker := []byte(scanOptionsOf(q).Encode() + "\n")
		_ = os.WriteFile(filepath.Join(s.opts.NotesDir, scanMarker), marker, 0o644)
	}
	return res, nil
}

// explainBudget is how many folders of the manuals the model may explain in one scan: what the
// person chose, within manualExplainLimit, or manualExplainMax when they chose nothing valid.
func explainBudget(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return manualExplainMax
	}
	return min(n, manualExplainLimit)
}

// scanModelsFor says what the model may explain: nothing unless the person asked for it and a
// service can; otherwise a few projects and up to modules folders of their manuals.
func (s *Server) scanModelsFor(on bool, modules int) scanModels {
	if !on {
		return scanModels{}
	}
	d := s.archDescriber()
	if d == nil {
		return scanModels{}
	}
	return scanModels{
		project: &archModel{svc: d, left: archDescribeMax},
		module:  &archModel{svc: d, left: modules, module: true},
	}
}
