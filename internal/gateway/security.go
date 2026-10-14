package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/sandbox"
)

// HOW MUCH OF THE DISK THE AGENT REACHES, as the settings page's Security tab shows and changes it.
//
// The choice is sandbox.disk_access (see config/diskaccess.go): the workspace alone, the default,
// or the whole disk. It is saved in the configuration file and handed to every open conversation at
// once, so the next command and the next file action of each follow it; a command already running
// keeps the rules it started with.
//
// Widening it to the whole disk is never done without an explicit confirmation in the request
// itself, like the network exposure: the page tells the person what it means first and sends
// confirm only after they said yes. Going back to the workspace narrows what the agent can do and
// needs nothing.

// securityView is what GET and PUT /v1/security answer.
type securityView struct {
	// DiskAccess is the configuration file's choice, or the running one when there is no file.
	DiskAccess string `json:"disk_access"`
	// CanSave is false when there is no configuration file to save a change in.
	CanSave bool `json:"can_save"`
	// Enforced says the kernel confines the agent's commands too (Linux with Landlock, write
	// confinement on, no chroot). Without it the workspace limit holds for motita's own file
	// actions only, and the page says so.
	Enforced bool `json:"enforced"`
}

// diskAccessSetter is a service that can take a new disk access for its next command.
type diskAccessSetter interface {
	SetDiskAccess(mode string)
}

// setDiskAccess and confinementAvailable are the config and sandbox packages', as variables so a
// test can make the write fail and choose what the kernel offers.
var (
	setDiskAccess        = config.SetDiskAccess
	confinementAvailable = sandbox.ConfinementAvailable
)

// securityConfig is the configuration the setting is read from: the file's when there is one, the
// running one otherwise.
func (s *Server) securityConfig() (config.Config, bool, error) {
	if s.opts.ConfigPath == "" {
		return s.opts.Service.Config(), false, nil
	}
	cfg, err := loadConfig(s.opts.ConfigPath)
	return cfg, true, err
}

// writeSecurity answers the current view, or the error that kept it from being read.
func (s *Server) writeSecurity(w http.ResponseWriter) {
	cfg, canSave, err := s.securityConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the configuration could not be read: "+err.Error())
		return
	}
	mode := config.DiskWorkspace
	if cfg.Sandbox.FullDisk() {
		mode = config.DiskFull
	}
	writeJSON(w, http.StatusOK, securityView{
		DiskAccess: mode,
		CanSave:    canSave,
		Enforced:   confinementAvailable() && cfg.Sandbox.ConfineWrites && cfg.Sandbox.Kind != "chroot",
	})
}

// handleGetSecurity answers how much of the disk the agent reaches.
func (s *Server) handleGetSecurity(w http.ResponseWriter, _ *http.Request) {
	s.writeSecurity(w)
}

// handlePutSecurity saves a new disk access and hands it to every open conversation.
func (s *Server) handlePutSecurity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DiskAccess string `json:"disk_access"`
		Confirm    bool   `json:"confirm"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.DiskAccess))
	if !config.ValidDiskAccess(mode) {
		writeError(w, http.StatusBadRequest, "disk_access must be workspace or full")
		return
	}
	if mode == config.DiskFull && !body.Confirm {
		writeError(w, http.StatusBadRequest, "giving the agent the whole disk needs confirm: true, "+
			"sent after the person has been told what the change means")
		return
	}
	if !s.needFile(w) {
		return
	}
	if err := setDiskAccess(s.opts.ConfigPath, mode); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range s.snapshot() {
		applyDiskAccess(c.svc, mode)
	}
	s.info("the agent's disk access was changed from the settings", "disk_access", mode)
	s.writeSecurity(w)
}

// startingDiskAccess hands a new conversation the disk access the file names now: the conversation
// was built from the configuration the gateway started with, and the settings may have changed it.
func (s *Server) startingDiskAccess(svc Service) {
	if s.opts.ConfigPath == "" {
		return
	}
	if cfg, err := loadConfig(s.opts.ConfigPath); err == nil {
		applyDiskAccess(svc, cfg.Sandbox.DiskAccess)
	}
}

// applyDiskAccess gives svc the disk access, when it can take one.
func applyDiskAccess(svc Service, mode string) {
	if ds, ok := svc.(diskAccessSetter); ok {
		ds.SetDiskAccess(mode)
	}
}
