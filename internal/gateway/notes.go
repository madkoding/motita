package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/notes"
)

// THE NOTE VAULT, as the Notes window shows and changes it: a folder of plain Markdown files that
// Obsidian opens as it is. The gateway only reads and writes files in that folder; a note changed on
// disk by another program is what the next request returns.

const errNoNotes = "this gateway does not keep notes"

// noteSearchLimit is how many passages a search answers.
const noteSearchLimit = 30

// noteView is what GET /v1/notes/note answers: the text, where its links lead and who links to it.
type noteView struct {
	notes.Note
	Body      string           `json:"body"`
	Links     []notes.Link     `json:"links"`
	Backlinks []notes.Backlink `json:"backlinks"`
}

// noteSave is what PUT /v1/notes/note takes. Create makes it fail when the note already exists.
type noteSave struct {
	Body   string `json:"body"`
	Create bool   `json:"create"`
}

// noteMove is what POST /v1/notes/rename takes.
type noteMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// noteHit is one passage of a search.
type noteHit struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// vault is the vault, or nil after answering that there is none.
func (s *Server) vault(w http.ResponseWriter) *notes.Vault {
	if s.opts.NotesDir == "" {
		writeError(w, http.StatusNotImplemented, errNoNotes)
		return nil
	}
	return notes.Open(s.opts.NotesDir)
}

// writeNoteError answers with the status that fits what went wrong.
func writeNoteError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, notes.ErrBadPath):
		status = http.StatusBadRequest
	case errors.Is(err, notes.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, notes.ErrExists), errors.Is(err, notes.ErrTooMany):
		status = http.StatusConflict
	case errors.Is(err, notes.ErrTooBig):
		status = http.StatusRequestEntityTooLarge
	default:
		// anything else is the server's fault: keep the 500
	}
	writeError(w, status, err.Error())
}

// handleListNotes answers every note of the vault and where the folder is, so the person can open
// it in another program.
func (s *Server) handleListNotes(w http.ResponseWriter, _ *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": v.Root(), "notes": v.List()})
}

// handleGetNote answers one note with its links and backlinks.
func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	p := r.URL.Query().Get("path")
	body, modified, err := v.Read(p)
	if err != nil {
		writeNoteError(w, err)
		return
	}
	rel, _ := notes.Clean(p)
	view := noteView{
		Note:      notes.Note{Path: rel, Title: notes.Title(rel), Size: int64(len(body)), Modified: modified},
		Body:      body,
		Links:     notes.LinksOf(v.List(), body),
		Backlinks: v.Backlinks(rel),
	}
	writeJSON(w, http.StatusOK, view)
}

// handlePutNote saves a note, creating it and its folders when it is new.
func (s *Server) handlePutNote(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	var body noteSave
	if !s.decodeBody(w, r, &body) {
		return
	}
	n, err := v.Write(r.URL.Query().Get("path"), body.Body, body.Create)
	if err != nil {
		writeNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleDeleteNote removes a note.
func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	if err := v.Delete(r.URL.Query().Get("path")); err != nil {
		writeNoteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameNote moves a note to another path and answers how many notes had a link rewritten.
func (s *Server) handleRenameNote(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	var body noteMove
	if !s.decodeBody(w, r, &body) {
		return
	}
	updated, err := v.RenameLinked(body.From, body.To)
	if err != nil {
		writeNoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// handleNotesGraph answers the notes and the links between them, for the graph view.
func (s *Server) handleNotesGraph(w http.ResponseWriter, _ *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	writeJSON(w, http.StatusOK, v.Graph())
}

// handleSearchNotes finds notes by what they say. With the code index on it ranks by meaning, with
// the same index the agent searches the code with; with it off, every word has to appear.
func (s *Server) handleSearchNotes(w http.ResponseWriter, r *http.Request) {
	v := s.vault(w)
	if v == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := noteSearchLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < limit {
		limit = n
	}
	out := []noteHit{}
	if q != "" {
		out = s.searchNotes(r, v, q, limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": out})
}

// searchNotes ranks the vault's notes against q.
func (s *Server) searchNotes(r *http.Request, v *notes.Vault, q string, limit int) []noteHit {
	out := []noteHit{}
	if cfg, _, err := s.securityConfig(); err == nil && cfg.CodeIndex.Enabled && len(v.List()) > 0 {
		hits, err := codeindex.Shared(cfg.CodeIndex.Options()).Search(r.Context(), v.Root(), q, limit)
		if err == nil {
			for _, h := range hits {
				if _, err := notes.Clean(h.Path); err == nil {
					out = append(out, noteHit{
						Path: h.Path, Title: notes.Title(h.Path), Line: h.Line,
						Snippet: firstSnippetLine(h.Snippet),
					})
				}
			}
			return out
		}
	}
	for _, h := range v.Search(q, limit) {
		out = append(out, noteHit{Path: h.Path, Title: h.Title, Line: h.Line, Snippet: h.Snippet})
	}
	return out
}

// firstSnippetLine is the first non-empty line of a passage.
func firstSnippetLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// notesRoutes is the table of the workspace notes: reading, writing, search, the map and the scan.
func (s *Server) notesRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/notes", plain(s.handleListNotes))
	mux.Handle("GET /v1/notes/note", plain(s.handleGetNote))
	mux.Handle("PUT /v1/notes/note", plain(s.handlePutNote))
	mux.Handle("DELETE /v1/notes/note", plain(s.handleDeleteNote))
	mux.Handle("POST /v1/notes/rename", plain(s.handleRenameNote))
	mux.Handle("GET /v1/notes/search", plain(s.handleSearchNotes))
	mux.Handle("GET /v1/notes/graph", plain(s.handleNotesGraph))
	mux.Handle("POST /v1/notes/scan", plain(s.handleScanNotes))
	mux.Handle("GET /v1/notes/scan", plain(s.handleScanStatus))
}
