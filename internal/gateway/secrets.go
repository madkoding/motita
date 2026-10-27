package gateway

import (
	"net/http"

	"github.com/madkoding/motita/internal/config"
)

// THE VARIABLES KEPT FOR THE AGENT, as Settings → Secrets lists and changes them.
//
// A variable is a NAME and a value the agent's commands get in their environment (see
// config/secrets.go). The API never returns a value: a name comes back with, at most, the last
// characters of a long one so the person can tell two apart. A change reaches every open
// conversation from its next command, since the sandbox reads the file at each run.

// secretView is one variable as the page sees it.
type secretView struct {
	Name string `json:"name"`
	// Hint is the last four characters of a value of 16 or more, "" for a shorter one.
	Hint string `json:"hint,omitempty"`
}

// secretsView is what the secrets endpoints answer.
type secretsView struct {
	// CanSave is false when there is no configuration file to keep the variables beside.
	CanSave bool         `json:"can_save"`
	Secrets []secretView `json:"secrets"`
	Max     int          `json:"max"`
}

// The write, as a variable so a test can make it fail.
var setSecret = config.SetSecret

func (s *Server) secretsRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/secrets", plain(s.handleSecrets))
	mux.Handle("PUT /v1/secrets/{name}", plain(s.handleSecretSave))
	mux.Handle("DELETE /v1/secrets/{name}", plain(s.handleSecretForget))
}

// secretsSnapshot is the view of the file beside the configuration.
func (s *Server) secretsSnapshot() secretsView {
	values := config.ReadSecrets(s.opts.ConfigPath)
	out := secretsView{CanSave: s.opts.ConfigPath != "", Secrets: []secretView{}, Max: config.MaxSecrets}
	for _, n := range config.SecretNames(values) {
		v := secretView{Name: n}
		if len(values[n]) >= 16 {
			v.Hint = values[n][len(values[n])-4:]
		}
		out.Secrets = append(out.Secrets, v)
	}
	return out
}

func (s *Server) handleSecrets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.secretsSnapshot())
}

func (s *Server) handleSecretSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value string `json:"value"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	name := r.PathValue("name")
	if msg := config.CheckSecretName(name); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "send the value; to remove the variable, delete it")
		return
	}
	if !s.needFile(w) {
		return
	}
	if err := setSecret(s.opts.ConfigPath, name, body.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.secretsSnapshot())
}

func (s *Server) handleSecretForget(w http.ResponseWriter, r *http.Request) {
	if !s.needFile(w) {
		return
	}
	if err := setSecret(s.opts.ConfigPath, r.PathValue("name"), ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.secretsSnapshot())
}
