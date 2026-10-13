package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/tailscale"
)

// THE GATEWAY'S REACH, as the settings page shows and changes it.
//
// Two answers travel together: what the configuration file says (the exposure the NEXT start
// binds) and what this process is bound to now. They differ after a change, because a socket is
// not moved under the clients that are using it: the new address is written to the file and
// applies when the gateway restarts, and the page says so instead of pretending it already did.
//
// A change is never made without an explicit confirmation in the request itself. The page asks
// the person first and sends confirm only after they said yes, and a client that skips the
// question is refused here rather than trusted to have asked it.

// TailscaleService is what the gateway needs from Tailscale. *tailscale.Client is the real one.
type TailscaleService interface {
	Detect(ctx context.Context, target string) tailscale.Status
	Serve(ctx context.Context, target string) error
	Unserve(ctx context.Context) error
}

// networkView is what GET and PUT /v1/network and POST /v1/network/tailscale answer.
type networkView struct {
	// Mode and Listen are the configuration file's: what the next start binds.
	Mode   string `json:"mode"`
	Listen string `json:"listen"`
	// ActiveMode and ActiveListen are this process's: what is bound now.
	ActiveMode   string `json:"active_mode"`
	ActiveListen string `json:"active_listen"`
	// RestartNeeded says the two differ, so the saved exposure applies when the gateway restarts.
	RestartNeeded bool `json:"restart_needed"`
	// CanSave is false when there is no configuration file to save a change in.
	CanSave   bool             `json:"can_save"`
	Tailscale tailscale.Status `json:"tailscale"`
}

// setGatewayExposure and loadConfig are the config package's, as variables so a test can make
// the write or the read fail without arranging a broken filesystem.
var (
	setGatewayExposure = config.SetGatewayExposure
	loadConfig         = config.LoadWithoutKey
)

// knowsHost reports whether host is one of the names this gateway answers to.
func (s *Server) knowsHost(host string) bool {
	s.hostsMu.RLock()
	defer s.hostsMu.RUnlock()
	return s.hosts[host]
}

// addHost makes the gateway answer to one more name of its own.
func (s *Server) addHost(host string) {
	if host = normalHost(host); host == "" {
		return
	}
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.hosts[host] = true
}

// learnTailnetName adds this machine's tailnet name to the names it answers to, when Tailscale is
// running.
func (s *Server) learnTailnetName(ctx context.Context) {
	s.addHost(s.opts.Tailscale.Detect(ctx, s.tailscaleTarget()).DNSName)
}

// tailscaleTarget is where `tailscale serve` should forward: the address actually bound, so a
// gateway on port 0 or on the wildcard is reached where it really is.
func (s *Server) tailscaleTarget() string {
	return tailscale.Target(s.listener.Addr().String())
}

// tailscaleStatus is Tailscale as this machine has it, or the zero status when it is not looked for.
func (s *Server) tailscaleStatus(ctx context.Context) tailscale.Status {
	if s.opts.Tailscale == nil {
		return tailscale.Status{}
	}
	return s.opts.Tailscale.Detect(ctx, s.tailscaleTarget())
}

// networkView builds the answer from the file, this process and Tailscale.
func (s *Server) networkView(ctx context.Context) (networkView, error) {
	active := config.Gateway{Listen: s.opts.Listen, Allow: s.opts.AllowRules}
	v := networkView{
		ActiveMode:   active.Exposure(),
		ActiveListen: s.listener.Addr().String(),
		Tailscale:    s.tailscaleStatus(ctx),
	}
	saved := active
	if s.opts.ConfigPath != "" {
		cfg, err := loadConfig(s.opts.ConfigPath)
		if err != nil {
			return v, err
		}
		saved = cfg.Gateway
		v.CanSave = true
	}
	v.Mode = saved.Exposure()
	v.Listen = saved.ListenAddress()
	v.RestartNeeded = v.Mode != v.ActiveMode || saved.Port() != active.Port()
	return v, nil
}

// writeNetwork answers the current view, or the error that kept it from being read.
func (s *Server) writeNetwork(w http.ResponseWriter, r *http.Request) {
	v, err := s.networkView(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the configuration could not be read: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleGetNetwork answers how far the gateway reaches, now and after a restart, and Tailscale.
func (s *Server) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	s.writeNetwork(w, r)
}

// handlePutNetwork saves a new exposure in the configuration file. It applies at the next start.
func (s *Server) handlePutNetwork(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode    string `json:"mode"`
		Confirm bool   `json:"confirm"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if !config.ValidExposure(mode) {
		writeError(w, http.StatusBadRequest, "the mode must be local, lan or all")
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "changing who can reach the gateway needs confirm: true, "+
			"sent after the person has been told what the change means")
		return
	}
	if s.opts.ConfigPath == "" {
		writeError(w, http.StatusConflict, "this gateway runs on the built-in defaults and has no configuration file "+
			"to save the address in: run the setup (motita config) or start it with -config")
		return
	}
	if err := setGatewayExposure(s.opts.ConfigPath, mode); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.info("the gateway's exposure was changed from the settings; it applies at the next start", "mode", mode)
	s.writeNetwork(w, r)
}

// handleTailscale turns the gateway's publication inside the tailnet on or off.
func (s *Server) handleTailscale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enable bool `json:"enable"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	st := s.tailscaleStatus(r.Context())
	if !st.Running {
		writeError(w, http.StatusConflict, "Tailscale is not running on this machine")
		return
	}
	var err error
	if body.Enable {
		// The name first: the first request under it may follow the command at once.
		s.addHost(st.DNSName)
		err = s.opts.Tailscale.Serve(r.Context(), s.tailscaleTarget())
	} else {
		err = s.opts.Tailscale.Unserve(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.info("the gateway's tailnet publication was changed from the settings", "enabled", body.Enable)
	s.writeNetwork(w, r)
}
