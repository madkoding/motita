package gateway

import (
	"cmp"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/madkoding/motita/internal/memory"
)

// THE MEMORY, as the Memory window shows and changes it: a folder of plain Markdown files that
// Obsidian opens as it is. The first folder of an entry is its namespace (a collection). The
// gateway only reads and writes files in that folder; an entry changed on disk by another program
// is what the next request returns.

const errNoMemory = "this gateway does not keep a memory"

// memoryView is what GET /v1/memory/entry answers: the text, where its links lead and who links to it.
type memoryView struct {
	memory.Entry
	Meta      memory.Meta       `json:"meta"`
	Body      string            `json:"body"`
	Links     []memory.Link     `json:"links"`
	Backlinks []memory.Backlink `json:"backlinks"`
}

// memorySave is what PUT /v1/memory/entry takes. Create makes it fail when the entry already exists.
type memorySave struct {
	Body   string `json:"body"`
	Create bool   `json:"create"`
}

// memoryMove is what POST /v1/memory/rename takes.
type memoryMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// memory is the store, or nil after answering that there is none.
func (s *Server) memory(w http.ResponseWriter) *memory.Store {
	if s.opts.MemoryDir == "" {
		writeError(w, http.StatusNotImplemented, errNoMemory)
		return nil
	}
	return memory.Open(s.opts.MemoryDir)
}

// writeMemoryError answers with the status that fits what went wrong.
func writeMemoryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, memory.ErrBadPath):
		status = http.StatusBadRequest
	case errors.Is(err, memory.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, memory.ErrExists), errors.Is(err, memory.ErrTooMany):
		status = http.StatusConflict
	case errors.Is(err, memory.ErrTooBig):
		status = http.StatusRequestEntityTooLarge
	default:
		// anything else is the server's fault: keep the 500
	}
	writeError(w, status, err.Error())
}

// handleListMemory answers every entry of the memory, its namespaces, and where it is, so the person
// can open it in another program.
func (s *Server) handleListMemory(w http.ResponseWriter, _ *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": m.Root(), "entries": m.List(), "namespaces": m.Namespaces()})
}

// handleGetMemory answers one entry with its metadata, links and backlinks.
func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	p := r.URL.Query().Get("path")
	body, modified, err := m.Read(p)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	rel, _ := memory.Clean(p)
	meta, _, _ := memory.ParseMeta(body, modified)
	writeJSON(w, http.StatusOK, memoryView{
		Entry:     memory.Entry{Path: rel, Title: memory.Title(rel), Size: int64(len(body)), Modified: modified},
		Meta:      meta,
		Body:      body,
		Links:     memory.LinksOf(m.List(), body),
		Backlinks: m.Backlinks(rel),
	})
}

// handlePutMemory saves an entry, creating it and its folders when it is new.
func (s *Server) handlePutMemory(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	var body memorySave
	if !s.decodeBody(w, r, &body) {
		return
	}
	e, err := m.Write(r.URL.Query().Get("path"), body.Body, body.Create)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleDeleteMemory removes an entry.
func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	if err := m.Delete(r.URL.Query().Get("path")); err != nil {
		writeMemoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteMemoryFolder removes the entries of a folder (a namespace, or a folder inside one),
// sub-folders included, and answers how many.
func (s *Server) handleDeleteMemoryFolder(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	n, err := m.DeleteFolder(r.URL.Query().Get("path"))
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

// handleRenameMemory moves an entry to another path and answers how many entries had a link rewritten.
func (s *Server) handleRenameMemory(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	var body memoryMove
	if !s.decodeBody(w, r, &body) {
		return
	}
	updated, err := m.RenameLinked(body.From, body.To)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// handleMemoryGraph answers the entries and the links between them, for the map.
func (s *Server) handleMemoryGraph(w http.ResponseWriter, _ *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	writeJSON(w, http.StatusOK, m.Graph())
}

// handleSearchMemory recalls what the memory says about q. The query may carry its filters in the
// text (ns:Name author:Name since:2026-01-31 until:2026-12-31 graph:1) or in the parameters
// namespace, author, since, until and graph, which win. graph=1 adds the entries related through
// links.
func (s *Server) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	v := r.URL.Query()
	q := memory.ParseQuery(v.Get("q"))
	q.Namespace = cmp.Or(v.Get("namespace"), q.Namespace)
	q.Author = cmp.Or(v.Get("author"), q.Author)
	if t, ok := memory.ParseDate(v.Get("since")); ok {
		q.Since = t
	}
	if t, ok := memory.ParseDate(v.Get("until")); ok {
		q.Until = t.Add(24*time.Hour - time.Nanosecond)
	}
	q.Graph = q.Graph || v.Get("graph") == "1"
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	if cfg, _, err := s.securityConfig(); err == nil {
		m = m.WithEmbedder(cfg.CodeIndex.MemoryEmbedder)
	}
	writeJSON(w, http.StatusOK, m.Search(r.Context(), q))
}

// memoryRoutes is the table of the memory: reading, writing, recall and the map.
func (s *Server) memoryRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/pull-requests", plain(s.handleListPRs)) // the overview of prlist.go
	mux.Handle("POST /v1/pull-requests/update", plain(s.handleUpdatePRBranch))
	mux.Handle("GET /v1/memory", plain(s.handleListMemory))
	mux.Handle("GET /v1/memory/entry", plain(s.handleGetMemory))
	mux.Handle("PUT /v1/memory/entry", plain(s.handlePutMemory))
	mux.Handle("DELETE /v1/memory/entry", plain(s.handleDeleteMemory))
	mux.Handle("DELETE /v1/memory/folder", plain(s.handleDeleteMemoryFolder))
	mux.Handle("POST /v1/memory/rename", plain(s.handleRenameMemory))
	mux.Handle("GET /v1/memory/search", plain(s.handleSearchMemory))
	mux.Handle("GET /v1/memory/graph", plain(s.handleMemoryGraph))
}
