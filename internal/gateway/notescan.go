package gateway

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	// Skipped counts sessions with nothing said yet, and notes the vault refused.
	Skipped int `json:"skipped"`
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
}

func (s *Server) scanSessions() []scanSession {
	var out []scanSession
	for _, c := range s.snapshot() {
		c.stateMu.Lock()
		ss := scanSession{id: c.id, title: c.title, projectID: c.projectID, created: c.created, lastUsed: c.lastUsed}
		c.stateMu.Unlock()
		if c.svc != nil {
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

func sessionBlock(ss scanSession, projectLink string) string {
	var b strings.Builder
	b.WriteString("## Session\n")
	if projectLink != "" {
		fmt.Fprintf(&b, "- Project: %s\n", projectLink)
	}
	fmt.Fprintf(&b, "- Started: %s\n- Last used: %s\n- Turns: %d\n",
		ss.created.Format(scanTimeFormat), ss.lastUsed.Format(scanTimeFormat), len(ss.turns))
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

func projectBlock(p Project, links []string) string {
	var b strings.Builder
	b.WriteString("## Project\n")
	fmt.Fprintf(&b, "- Folder: `%s`\n", p.Dir)
	if p.GitURL != "" {
		fmt.Fprintf(&b, "- Repository: %s\n", redact.String(p.GitURL))
	}
	if p.MainBranch != "" {
		fmt.Fprintf(&b, "- Main branch: %s\n", p.MainBranch)
	}
	if p.Description != "" {
		b.WriteString("\n" + quote(p.Description, scanQuoteBytes) + "\n")
	}
	if len(links) > 0 {
		b.WriteString("\n### Sessions\n")
		for _, l := range links {
			b.WriteString("- " + l + "\n")
		}
	}
	if readme := readmeExcerpt(p.Dir); readme != "" {
		b.WriteString("\n### README\n" + readme + "\n")
	}
	return b.String()
}

// handleScanNotes writes a note for every project and every session that has been used, and
// answers what it did. Running it again updates the same notes.
func (s *Server) handleScanNotes(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	marker := filepath.Join(s.opts.NotesDir, scanMarker)
	if r.URL.Query().Get("first") != "" {
		// Only the first time: a vault that has notes, or was scanned before (and emptied since), is
		// the person's to fill.
		if _, err := os.Stat(marker); err == nil || len(v.List()) > 0 {
			writeJSON(w, http.StatusOK, noteScan{})
			return
		}
	}
	var projects []Project
	if s.projects != nil {
		all, err := s.projects.loadAll()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		projects = all
	}
	// Two projects can share a title: the second one is told apart by the start of its id.
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
	link := func(name string) string { return "[[" + strings.TrimSuffix(name, ".md") + "]]" }

	res := noteScan{Ran: true}
	byProject := map[string][]string{}
	for _, ss := range s.scanSessions() {
		if len(ss.turns) == 0 {
			res.Skipped++
			continue
		}
		name := sessionNoteName(ss.title, ss.id)
		if name == "" {
			res.Skipped++
			continue
		}
		res.Sessions++
		projectLink := ""
		if pn, ok := names[ss.projectID]; ok {
			projectLink = link(pn)
			byProject[ss.projectID] = append(byProject[ss.projectID], link(name))
		}
		outcome, err := v.Upsert(name, strings.TrimSuffix(filepath.Base(name), ".md"), sessionBlock(ss, projectLink))
		res.count(outcome, err)
	}
	for _, p := range projects {
		res.Projects++
		name := names[p.ID]
		outcome, err := v.Upsert(name, p.Title, projectBlock(p, byProject[p.ID]))
		res.count(outcome, err)
	}
	// Remembered in the vault folder, as a hidden file notes never list, so any client sees it.
	if os.MkdirAll(s.opts.NotesDir, 0o755) == nil {
		_ = os.WriteFile(marker, []byte("scanned\n"), 0o644)
	}
	writeJSON(w, http.StatusOK, res)
}
