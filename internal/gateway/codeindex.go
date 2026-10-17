package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/config"
)

// THE CODE INDEX, as the settings page's Code search tab shows and changes it: whether search_index
// is on, the ranking by meaning, the embeddings and the limits (the code_index block).
//
// A change is saved in the configuration file and reaches the sessions started after it; a session
// that is open keeps the index it was built with. Pointing the embeddings at an address that is not
// on this machine or its network SENDS THE TEXT OF THE INDEXED FILES there, so it is never done
// without confirm in the request itself, sent only after the person was told what it means.
// The embeddings key is not handled here: it belongs in MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY.

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
		writeError(w, http.StatusInternalServerError, "the configuration could not be read: "+err.Error())
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
