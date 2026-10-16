package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/madkoding/motita/internal/voice"
)

// LOCAL VOICES: Piper voices the page runs itself (see voice.Library).
//
// The page lists the catalog, asks for one voice by id, and fetches the files from here. The
// gateway downloads from the catalog's own addresses only, checks every file's hash, and serves
// the files from this origin, so the page's policy keeps naming nothing but 'self'.
// PROCESS-WIDE, like the other voice endpoints.

// localModelView is one voice of the catalog as GET /v1/voice/models answers it.
type localModelView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Lang    string `json:"lang"`
	Quality string `json:"quality"`
	Engine  string `json:"engine"`
	// Size is what downloading it takes now: its files and the runtime it lacks.
	Size      int64 `json:"size"`
	Installed bool  `json:"installed"`
	// Sample says GET /v1/voice/models/{id}/sample plays it before it is downloaded.
	Sample bool `json:"sample"`
}

// downloadView is the download in progress, or the last one when it failed.
type downloadView struct {
	ID    string `json:"id"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

// modelDownload is the one download this gateway runs at a time.
type modelDownload struct {
	mu      sync.Mutex
	running bool
	view    *downloadView
}

// snapshot is a copy of the download's state, nil when there is none to report.
func (d *modelDownload) snapshot() *downloadView {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.view == nil {
		return nil
	}
	v := *d.view
	return &v
}

// errNoLocalVoices is the answer of every local-voice endpoint on a gateway without a library.
const errNoLocalVoices = "this gateway keeps no local voices"

// handleVoiceModels answers the catalog, what is on disk and the download in progress.
func (s *Server) handleVoiceModels(w http.ResponseWriter, r *http.Request) {
	lib := s.opts.VoiceModels
	if lib == nil {
		writeError(w, http.StatusNotImplemented, errNoLocalVoices)
		return
	}
	models := make([]localModelView, 0, len(lib.Models))
	for _, m := range lib.Models {
		models = append(models, localModelView{ID: m.ID, Name: m.Name, Lang: m.Lang, Quality: m.Quality,
			Engine: m.Engine, Size: lib.DownloadSize(m), Installed: lib.Installed(m.ID), Sample: m.Sample != nil})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime":  map[string]any{"size": lib.RuntimeSize(), "installed": lib.RuntimeInstalled()},
		"models":   models,
		"download": s.voiceDownload.snapshot(),
	})
}

// handleInstallVoiceModel starts downloading one voice of the catalog. It answers at once: the
// page follows the progress in GET /v1/voice/models.
func (s *Server) handleInstallVoiceModel(w http.ResponseWriter, r *http.Request) {
	lib, id, ok := s.voiceModelOf(w, r)
	if !ok {
		return
	}
	if lib.Installed(id) {
		writeJSON(w, http.StatusOK, map[string]any{"installed": true})
		return
	}
	d := &s.voiceDownload
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		writeError(w, http.StatusConflict, "another voice is downloading: wait for it to finish")
		return
	}
	d.running = true
	d.view = &downloadView{ID: id}
	d.mu.Unlock()
	go s.downloadVoiceModel(s.baseCtx, *lib, id)
	writeJSON(w, http.StatusAccepted, map[string]any{"installed": false})
}

// downloadVoiceModel runs one download and records how it went.
func (s *Server) downloadVoiceModel(ctx context.Context, lib voice.Library, id string) {
	d := &s.voiceDownload
	err := lib.Install(ctx, id, func(done, total int64) {
		d.mu.Lock()
		d.view.Done, d.view.Total = done, total
		d.mu.Unlock()
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = false
	if err != nil {
		d.view.Error = err.Error()
		s.warn("a local voice could not be downloaded", "voice", id, "error", err.Error())
		return
	}
	d.view = nil
}

// handleDeleteVoiceModel removes a voice's files from this gateway.
func (s *Server) handleDeleteVoiceModel(w http.ResponseWriter, r *http.Request) {
	lib, id, ok := s.voiceModelOf(w, r)
	if !ok {
		return
	}
	d := &s.voiceDownload
	d.mu.Lock()
	busy := d.running && d.view.ID == id
	d.mu.Unlock()
	if busy {
		writeError(w, http.StatusConflict, "this voice is downloading: wait for it to finish")
		return
	}
	if err := lib.Remove(id); err != nil {
		writeError(w, http.StatusInternalServerError, "the voice could not be removed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// voiceModelOf is the library and the catalog id a request names, or false after answering why
// there is none.
func (s *Server) voiceModelOf(w http.ResponseWriter, r *http.Request) (*voice.Library, string, bool) {
	lib := s.opts.VoiceModels
	if lib == nil {
		writeError(w, http.StatusNotImplemented, errNoLocalVoices)
		return nil, "", false
	}
	id := r.PathValue("id")
	if _, ok := lib.Find(id); !ok {
		writeError(w, http.StatusNotFound, voice.ErrUnknownModel.Error())
		return nil, "", false
	}
	return lib, id, true
}

// modelFileTypes are the content types of the files a local voice is made of. A browser
// compiles WebAssembly as it streams in only when it is served as application/wasm.
var modelFileTypes = map[string]string{
	".wasm": "application/wasm",
	".json": "application/json",
	".mp3":  "audio/mpeg",
}

// handleVoiceModelFile serves one downloaded file: a voice's (by its id) or the runtime's.
func (s *Server) handleVoiceModelFile(w http.ResponseWriter, r *http.Request) {
	lib := s.opts.VoiceModels
	if lib == nil {
		writeError(w, http.StatusNotImplemented, errNoLocalVoices)
		return
	}
	path, ok := lib.File(r.PathValue("id"), r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "that file is not downloaded")
		return
	}
	ctype := modelFileTypes[filepath.Ext(path)]
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	// The page keeps its own copy (Cache Storage) across updates and asks again with its tag: the
	// pinned hash answers 304 while the catalog keeps the file, and the new bytes once it pins
	// another.
	w.Header().Set("Cache-Control", "private, no-cache")
	if sum := lib.Sum(r.PathValue("id"), r.PathValue("name")); sum != "" {
		w.Header().Set("ETag", `"`+sum+`"`)
	}
	http.ServeFile(w, r, path)
}

// handleVoiceModelSample plays a voice before it is downloaded: the gateway fetches its sample
// from the catalog's address the first time, checks it like any other file, and keeps it.
func (s *Server) handleVoiceModelSample(w http.ResponseWriter, r *http.Request) {
	lib, id, ok := s.voiceModelOf(w, r)
	if !ok {
		return
	}
	path, err := lib.Sample(r.Context(), id)
	if errors.Is(err, voice.ErrNoSample) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "the sample could not be downloaded: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", modelFileTypes[filepath.Ext(path)])
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}
