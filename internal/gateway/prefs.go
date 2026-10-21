package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

// THE WEB PAGE'S PREFERENCES, kept by the gateway.
//
// A choice made in the settings (the look, the notifications, how the voice reads) belongs to the
// person and not to the browser they happened to use, so the page keeps it here and every browser
// reads the same values. What is tied to a device - the microphone, the downloaded voice models, the
// density of a phone - stays in that browser and is never sent.
//
// The values are opaque strings the page wrote (a small JSON document or on/off), saved under the
// key the page uses for them. Only the keys in prefKeys are kept, so the file cannot grow into a
// place for anything else.

// prefKeys are the preferences the gateway keeps.
var prefKeys = map[string]bool{
	"motita.ui.settings":      true,
	"motita.ui.theme":         true,
	"motita.ui.notifications": true,
	"motita.ui.ciToast":       true,
	"motita.ui.nextHint":      true,
	"motita.ui.usageStyle":    true,
	"motita.ui.usageOpen":     true,
	"motita.ui.speech":        true,
	"motita.ui.voice":         true,
}

// maxPrefBytes bounds one value: the largest, the pronunciation list, is a few kilobytes.
const maxPrefBytes = 32 << 10

// prefsStore is the preferences and the file they are written to; path is empty on a gateway that
// has no place to keep them, and then they last as long as the process.
type prefsStore struct {
	mu     sync.Mutex
	path   string
	values map[string]string
}

func openPrefs(path string) *prefsStore {
	p := &prefsStore{path: path, values: map[string]string{}}
	if path == "" {
		return p
	}
	if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
		var saved map[string]string
		if json.Unmarshal(raw, &saved) == nil {
			for k, v := range saved {
				if prefKeys[k] {
					p.values[k] = v
				}
			}
		}
	}
	return p
}

func (p *prefsStore) all() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(p.values))
	for k, v := range p.values {
		out[k] = v
	}
	return out
}

// merge saves the given values over the ones kept and writes the file before it answers: a choice
// reported as saved and lost at the next start is worse than an error.
func (p *prefsStore) merge(in map[string]string) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := make(map[string]string, len(p.values)+len(in))
	for k, v := range p.values {
		next[k] = v
	}
	for k, v := range in {
		next[k] = v
	}
	if p.path != "" {
		raw, err := json.Marshal(next)
		if err == nil {
			err = os.MkdirAll(filepath.Dir(p.path), 0o700)
		}
		if err == nil {
			err = writeFileAtomic(p.path, raw)
		}
		if err != nil {
			return nil, err
		}
	}
	p.values = next
	out := make(map[string]string, len(next))
	for k, v := range next {
		out[k] = v
	}
	return out, nil
}

// writeFileAtomic writes beside the file and renames, so a crash leaves the old one or the new one.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// routePrefs opens the preferences and serves them under /v1/ui/prefs.
func (s *Server) routePrefs(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	s.prefs = openPrefs(s.opts.PrefsPath)
	mux.Handle("GET /v1/ui/prefs", plain(s.handleGetPrefs))
	mux.Handle("PUT /v1/ui/prefs", plain(s.handlePutPrefs))
}

// prefsView is what GET and PUT /v1/ui/prefs answer.
type prefsView struct {
	Prefs map[string]string `json:"prefs"`
}

func (s *Server) handleGetPrefs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, prefsView{Prefs: s.prefs.all()})
}

// handlePutPrefs merges the values sent into the ones kept; a key not sent is left as it is.
func (s *Server) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	var body prefsView
	if !s.decodeBody(w, r, &body) {
		return
	}
	for k, v := range body.Prefs {
		if !prefKeys[k] {
			writeError(w, http.StatusBadRequest, "not a preference the gateway keeps: "+k)
			return
		}
		if len(v) > maxPrefBytes || !utf8.ValidString(v) {
			writeError(w, http.StatusBadRequest, "the value of "+k+" is too large or not text")
			return
		}
	}
	all, err := s.prefs.merge(body.Prefs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prefsView{Prefs: all})
}
