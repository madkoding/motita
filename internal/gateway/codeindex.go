package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/config"
)

// THE CODE INDEX, as the settings page's Indexing tab shows and changes it: whether search_index
// is on, the ranking by meaning, the embeddings and the limits (the code_index block).
//
// A change is saved in the configuration file and reaches the sessions started after it; a session
// that is open keeps the index it was built with. Pointing the embeddings at an address that is not
// on this machine or its network SENDS THE TEXT OF THE INDEXED FILES there, so it is never done
// without confirm in the request itself, sent only after the person was told what it means.
// The embeddings key is not handled here: it belongs in MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY.

// errConfigUnreadable starts the message when the configuration file cannot be read.
const errConfigUnreadable = "the configuration could not be read: "

// codeIndexView is what GET and PUT /v1/code-index answer.
type codeIndexView struct {
	Enabled         bool   `json:"enabled"`
	Semantic        bool   `json:"semantic"`
	EmbeddingsAuto  bool   `json:"embeddings_auto"`
	EmbeddingsURL   string `json:"embeddings_url"`
	EmbeddingsModel string `json:"embeddings_model"`
	// EmbeddingsKey says a key is set, never what it is.
	EmbeddingsKey bool `json:"embeddings_key"`
	MaxFileKB     int  `json:"max_file_kb"`
	MaxFiles      int  `json:"max_files"`
	// Embeddings is where the vectors come from: "endpoint" (embeddings_url), "auto" (a local Ollama
	// is looked for) or "off".
	Embeddings string `json:"embeddings"`
	// EndpointLocal says the endpoint is on this machine or its network; the text does not leave.
	EndpointLocal bool `json:"endpoint_local"`
	// CacheFiles and CacheBytes are the index files kept on disk and what they hold.
	CacheFiles int   `json:"cache_files"`
	CacheBytes int64 `json:"cache_bytes"`
	// CanSave is false when there is no configuration file to save a change in.
	CanSave bool `json:"can_save"`
}

// codeIndexChange is what PUT /v1/code-index takes: any of the fields, and confirm.
type codeIndexChange struct {
	Enabled         *bool   `json:"enabled"`
	Semantic        *bool   `json:"semantic"`
	EmbeddingsAuto  *bool   `json:"embeddings_auto"`
	EmbeddingsURL   *string `json:"embeddings_url"`
	EmbeddingsModel *string `json:"embeddings_model"`
	MaxFileKB       *int    `json:"max_file_kb"`
	MaxFiles        *int    `json:"max_files"`
	Confirm         bool    `json:"confirm"`
}

// refusal is why the change cannot be made as sent, "" when it can.
func (c codeIndexChange) refusal() string {
	switch {
	case c.empty():
		return "send at least one of enabled, semantic, embeddings_auto, embeddings_url, embeddings_model, " +
			"max_file_kb, max_files"
	case c.EmbeddingsURL != nil && strings.TrimSpace(*c.EmbeddingsURL) != "" &&
		!config.IsLocalURL(*c.EmbeddingsURL) && !c.Confirm:
		return "sending the text of the indexed files to an address outside this machine and its network " +
			"needs confirm: true, sent after the person has been told what the change means"
	default:
		return ""
	}
}

// empty says the change names no field.
func (c codeIndexChange) empty() bool {
	c.Confirm = false
	return c == codeIndexChange{}
}

// store is the change as the config package writes it, with the address and the model trimmed.
func (c codeIndexChange) store() config.CodeIndexChange {
	trim := func(s *string) *string {
		if s == nil {
			return nil
		}
		v := strings.TrimSpace(*s)
		return &v
	}
	return config.CodeIndexChange{
		Enabled: c.Enabled, Semantic: c.Semantic, EmbeddingsAuto: c.EmbeddingsAuto,
		EmbeddingsURL: trim(c.EmbeddingsURL), EmbeddingsModel: trim(c.EmbeddingsModel),
		MaxFileKB: c.MaxFileKB, MaxFiles: c.MaxFiles,
	}
}

// embeddingsSource says where the vectors come from: an address, a look-up for a local Ollama, or nowhere.
func embeddingsSource(x config.CodeIndex) string {
	switch {
	case x.EmbeddingsURL != "":
		return "endpoint"
	case x.EmbeddingsAuto:
		return "auto"
	default:
		return "off"
	}
}

// writeCodeIndex answers the current view, or the error that kept it from being read.
func (s *Server) writeCodeIndex(w http.ResponseWriter) {
	cfg, canSave, err := s.securityConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errConfigUnreadable+err.Error())
		return
	}
	x := cfg.CodeIndex
	view := codeIndexView{
		Enabled: x.Enabled, Semantic: x.Semantic, EmbeddingsAuto: x.EmbeddingsAuto,
		EmbeddingsURL: x.EmbeddingsURL, EmbeddingsModel: x.EmbeddingsModel, EmbeddingsKey: x.EmbeddingsAPIKey != "",
		MaxFileKB: x.MaxFileKB, MaxFiles: x.MaxFiles, CanSave: canSave,
		EndpointLocal: x.EmbeddingsURL != "" && config.IsLocalURL(x.EmbeddingsURL),
	}
	view.Embeddings = embeddingsSource(x)
	view.CacheFiles, view.CacheBytes = codeindex.CacheSize(x.CacheDir)
	writeJSON(w, http.StatusOK, view)
}

// handleGetCodeIndex answers the code index settings.
func (s *Server) handleGetCodeIndex(w http.ResponseWriter, _ *http.Request) {
	s.writeCodeIndex(w)
}

// handlePutCodeIndex saves the change in the configuration file.
func (s *Server) handlePutCodeIndex(w http.ResponseWriter, r *http.Request) {
	var body codeIndexChange
	if !s.decodeBody(w, r, &body) {
		return
	}
	if why := body.refusal(); why != "" {
		writeError(w, http.StatusBadRequest, why)
		return
	}
	if !s.needFile(w) {
		return
	}
	change := body.store()
	err := config.Transaction(s.opts.ConfigPath, func(tx config.Tx) error { return tx.SetCodeIndex(change) })
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.info("the code index settings were changed from the settings page")
	s.writeCodeIndex(w)
}

// codeIndexCleared is what DELETE /v1/code-index/cache answers.
type codeIndexCleared struct {
	Removed int   `json:"removed"`
	Bytes   int64 `json:"bytes"`
}

// handleDeleteCodeIndexCache deletes the index files kept in code_index.cache_dir. The next search
// builds the index again; nothing else is touched.
func (s *Server) handleDeleteCodeIndexCache(w http.ResponseWriter, _ *http.Request) {
	cfg, _, err := s.securityConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errConfigUnreadable+err.Error())
		return
	}
	files, size, err := codeindex.ClearCache(cfg.CodeIndex.CacheDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the index cache could not be cleared: "+err.Error())
		return
	}
	s.info("the code index cache was cleared from the settings page")
	writeJSON(w, http.StatusOK, codeIndexCleared{Removed: files, Bytes: size})
}

// handleProjectCodeIndex answers how much of the project's folder the index on disk covers.
func (s *Server) handleProjectCodeIndex(w http.ResponseWriter, r *http.Request) {
	s.projectIndex(w, r, false)
}

// handleBuildProjectCodeIndex builds (or refreshes) the project's index now, and answers its status.
func (s *Server) handleBuildProjectCodeIndex(w http.ResponseWriter, r *http.Request) {
	s.projectIndex(w, r, true)
}

// projectIndex is the two handlers above: build says whether the index is brought up to date first.
func (s *Server) projectIndex(w http.ResponseWriter, r *http.Request, build bool) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	cfg, _, err := s.securityConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errConfigUnreadable+err.Error())
		return
	}
	if !cfg.CodeIndex.Enabled {
		writeError(w, http.StatusConflict, "the code index is turned off in Settings → Indexing")
		return
	}
	opts := cfg.CodeIndex.Options()
	if build {
		if err := codeindex.Shared(opts).Build(r.Context(), p.Dir); err != nil {
			writeError(w, http.StatusInternalServerError, "the project could not be indexed: "+err.Error())
			return
		}
	}
	st, err := codeindex.Inspect(r.Context(), p.Dir, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the project could not be read: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.viewOf(p.Dir, st))
}

// indexNewProject indexes a project's folder in the background when the code index is on, so the
// first search_index in it is already warm. It is a courtesy: an index that is off or fails
// changes nothing, and it stops with the gateway.
func (s *Server) indexNewProject(dir string) {
	go s.indexFolder(dir)
}

// indexProjects, at start, brings the index of every project that has none or an old one up to
// date in the background, one after the other.
func (s *Server) indexProjects() {
	dirs := []string{}
	for _, p := range s.allProjects() {
		dirs = append(dirs, p.Dir)
	}
	if len(dirs) == 0 {
		return
	}
	go func() {
		for _, dir := range dirs {
			s.indexFolderIfOutdated(dir)
		}
	}()
}

// indexFolderIfOutdated builds the index of dir unless the one on disk already describes it.
func (s *Server) indexFolderIfOutdated(dir string) {
	cfg, _, err := s.securityConfig()
	if err != nil || !cfg.CodeIndex.Enabled {
		return
	}
	if st, err := codeindex.Inspect(s.baseCtx, dir, cfg.CodeIndex.Options()); err != nil || st.State == codeindex.StateReady {
		return
	}
	s.indexFolder(dir)
}

// indexFolder builds the index of dir if code_index.enabled is on. Errors are logged, not raised.
func (s *Server) indexFolder(dir string) {
	cfg, _, err := s.securityConfig()
	if err != nil || !cfg.CodeIndex.Enabled {
		return
	}
	s.indexing.Store(dir, true)
	defer s.indexing.Delete(dir)
	if err := codeindex.Shared(cfg.CodeIndex.Options()).Build(s.baseCtx, dir); err != nil {
		s.warn("the project could not be indexed in the background", "dir", dir, "error", err.Error())
	}
}

// projectIndexView is the status of a project's index, and whether it is being built right now.
type projectIndexView struct {
	codeindex.Status
	Indexing bool `json:"indexing"`
}

// viewOf adds to a status whether the folder is being indexed in the background.
func (s *Server) viewOf(dir string, st codeindex.Status) projectIndexView {
	_, busy := s.indexing.Load(dir)
	return projectIndexView{Status: st, Indexing: busy}
}
