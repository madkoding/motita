package gateway

import (
	"net/http"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/i18n"
)

// THE INTERFACE LANGUAGE, as one setting for every face of the program.
//
// The browser's settings window, the terminal's /language and the setup wizard all change the
// same ui.language, and each of them reaches it through here when a gateway is running: the
// value is written into the configuration file the gateway started with, so it outlives this
// process and the next interface to start reads it too. "auto" is resolved by each interface
// against its own environment - the terminal's locale, the browser's language - which is why the
// answer carries both the setting and what it resolves to on THIS host.

// uiView is what GET and PUT /v1/ui answer.
type uiView struct {
	// Language is the setting: auto, en or es.
	Language string `json:"language"`
	// Resolved is the language the setting means on the gateway's host: en or es.
	Resolved string `json:"resolved"`
}

// setUILanguage is config.SetUILanguage, as a variable so a test can make the write fail without
// arranging a read-only filesystem.
var setUILanguage = config.SetUILanguage

// uiSetting is the interface language in force: the last one PUT set, or the configuration's.
func (s *Server) uiSetting() string {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	if s.uiLanguage != "" {
		return s.uiLanguage
	}
	if l := strings.ToLower(strings.TrimSpace(s.opts.UILanguage)); l != "" && i18n.Valid(l) {
		return l
	}
	return i18n.Auto
}

func viewOfUI(setting string) uiView {
	return uiView{Language: setting, Resolved: string(i18n.Resolve(setting, os.Getenv))}
}

// handleGetUI answers the interface language.
func (s *Server) handleGetUI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, viewOfUI(s.uiSetting()))
}

// handlePutUI changes the interface language and saves it in the configuration file.
//
// The file is written BEFORE the answer changes: a language that was reported as saved and then
// lost at the next start is the one outcome worse than an error.
func (s *Server) handlePutUI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	lang := strings.ToLower(strings.TrimSpace(body.Language))
	if lang == "" || !i18n.Valid(lang) {
		writeError(w, http.StatusBadRequest, "the language must be auto, en or es")
		return
	}
	if s.opts.ConfigPath == "" {
		writeError(w, http.StatusConflict, "this gateway runs on the built-in defaults and has no configuration file "+
			"to save the language in: run the setup (motita -init) or start it with -config")
		return
	}
	if err := setUILanguage(s.opts.ConfigPath, lang); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.uiMu.Lock()
	s.uiLanguage = lang
	s.uiMu.Unlock()
	writeJSON(w, http.StatusOK, viewOfUI(lang))
}
