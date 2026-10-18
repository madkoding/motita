package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/desktop"
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
//
// The same page says whether the agent asks before the project's own code runs where nothing
// confines a command (sandbox.ask_unconfined_runs, on by default). Turning that off needs the same
// explicit confirmation, and turning it back on needs nothing.
//
// And whether the agent's commands run without a network (sandbox.isolate_network, on by default):
// once a command has read something, the network is how it would send it away. Turning it off needs
// the same confirmation; a command the user approves keeps the network either way.

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
	// AskUnconfinedRuns says the agent asks before the project's own code runs where nothing
	// confines a command (sandbox.ask_unconfined_runs).
	AskUnconfinedRuns bool `json:"ask_unconfined_runs"`
	// IsolateNetwork says the commands run without a network (sandbox.isolate_network), and
	// NetworkEnforced that this machine can do it: without it the setting changes nothing, and the
	// page says so.
	IsolateNetwork  bool `json:"isolate_network"`
	NetworkEnforced bool `json:"network_enforced"`
	// AutoApplySuggestions says the suggestions of a finished task are applied without asking
	// (agent.auto_apply_suggestions).
	AutoApplySuggestions bool `json:"auto_apply_suggestions"`
	// NetworkReason says why it cannot, and NetworkHint what would make it possible, when there is
	// one (Ubuntu 24.04: the AppArmor profile motita installs). Both are empty where it can.
	NetworkReason string `json:"network_reason,omitempty"`
	NetworkHint   string `json:"network_hint,omitempty"`
	// DesktopControl says the agent may see this computer's screen and use its mouse, keyboard and
	// windows, asking before every step (agent.desktop_control), and DesktopReason why there is no
	// desktop for it here ("" when there is one).
	DesktopControl bool   `json:"desktop_control"`
	DesktopReason  string `json:"desktop_reason,omitempty"`
}

// securityChange is what PUT /v1/security takes: a disk access, the unconfined-runs choice, the
// network isolation, any of them together, and the confirmation that widening any needs.
type securityChange struct {
	DiskAccess        string `json:"disk_access"`
	AskUnconfinedRuns *bool  `json:"ask_unconfined_runs"`
	IsolateNetwork    *bool  `json:"isolate_network"`
	// AutoApplySuggestions turns on applying every suggestion without asking.
	AutoApplySuggestions *bool `json:"auto_apply_suggestions"`
	// DesktopControl turns on the agent's control of this computer's desktop.
	DesktopControl *bool `json:"desktop_control"`
	Confirm        bool  `json:"confirm"`
}

// widens says whether the change turns off asking or the network isolation, or turns on applying
// suggestions without asking or the desktop control: what needs confirm.
func (c securityChange) widens() bool {
	return isFalse(c.AskUnconfinedRuns) || isFalse(c.IsolateNetwork) || isTrue(c.AutoApplySuggestions) ||
		isTrue(c.DesktopControl)
}

// empty says the change names nothing.
func (c securityChange) empty(mode string) bool {
	for _, t := range c.toggles() {
		if t.on != nil {
			return false
		}
	}
	return mode == ""
}

// isTrue and isFalse say a choice was sent and which way.
func isTrue(b *bool) bool { return b != nil && *b }

func isFalse(b *bool) bool { return b != nil && !*b }

// refusal is why the change cannot be made as sent ("" when it can); mode is its disk access,
// normalised.
func (c securityChange) refusal(mode string) string {
	switch {
	case c.empty(mode):
		return "send disk_access (workspace or full), ask_unconfined_runs, isolate_network, " +
			"auto_apply_suggestions, desktop_control, or several"
	case mode != "" && !config.ValidDiskAccess(mode):
		return "disk_access must be workspace or full"
	case mode == config.DiskFull && !c.Confirm:
		return "giving the agent the whole disk needs confirm: true, " +
			"sent after the person has been told what the change means"
	case c.widens() && !c.Confirm:
		return "running the project's code without asking where nothing confines it, giving the " +
			"commands the network, applying suggestions without asking, or letting the agent control " +
			"the desktop needs confirm: true, sent after the person has been told what the change means"
	default:
		return ""
	}
}

// networkIsolationSetter is a service that can take the network isolation for its next command.
type networkIsolationSetter interface {
	SetIsolateNetwork(on bool)
}

// unconfinedRunsSetter is a service that can take the unconfined-runs choice for its next command.
type unconfinedRunsSetter interface {
	SetAskUnconfinedRuns(ask bool)
}

// autoApplySetter is a service that can take the auto-apply choice for its next report.
type autoApplySetter interface {
	SetAutoApplySuggestions(on bool)
}

// desktopControlSetter is a service that can take the desktop control choice for its next turn.
type desktopControlSetter interface {
	SetDesktopControl(on bool)
}

// diskAccessSetter is a service that can take a new disk access for its next command.
type diskAccessSetter interface {
	SetDiskAccess(mode string)
}

// setDiskAccess and confinementAvailable are the config and sandbox packages', as variables so a
// test can make the write fail and choose what the kernel offers.
var (
	setDiskAccess        = config.Tx.SetDiskAccess
	setAskUnconfinedRuns = config.Tx.SetAskUnconfinedRuns
	setIsolateNetwork    = config.Tx.SetIsolateNetwork
	setAutoApply         = config.Tx.SetAutoApplySuggestions
	setDesktopControl    = config.Tx.SetDesktopControl
	desktopProbe         = probeDesktop
	confinementAvailable = sandbox.ConfinementAvailable
	networkAvailable     = sandbox.NetworkIsolationAvailable
	networkReason        = sandbox.NetworkIsolationReason
	networkHint          = sandbox.NetworkIsolationHint
)

// probeDesktop says why this computer has no desktop for the agent, or nil when it has one.
func probeDesktop() error {
	_, err := desktop.New()
	return err
}

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
	view := securityView{
		DiskAccess: mode,
		CanSave:    canSave,
		Enforced:   confinementAvailable() && cfg.Sandbox.ConfineWrites && cfg.Sandbox.Kind != "chroot",
		// The view always carries the choice: a missing key would read as false on the page.
		AskUnconfinedRuns: cfg.Sandbox.AskUnconfinedRuns,
		IsolateNetwork:    cfg.Sandbox.IsolateNetwork,
		NetworkEnforced:   networkAvailable(),

		AutoApplySuggestions: cfg.Agent.AutoApplySuggestions,
	}
	if !view.NetworkEnforced {
		view.NetworkReason, view.NetworkHint = networkReason(), networkHint()
	}
	view.DesktopControl = cfg.Agent.DesktopControl
	if err := desktopProbe(); err != nil {
		view.DesktopReason = err.Error()
	}
	writeJSON(w, http.StatusOK, view)
}

// handleGetSecurity answers how much of the disk the agent reaches.
func (s *Server) handleGetSecurity(w http.ResponseWriter, _ *http.Request) {
	s.writeSecurity(w)
}

// handlePutSecurity saves a new disk access, unconfined-runs choice or both, and hands them to
// every open conversation.
func (s *Server) handlePutSecurity(w http.ResponseWriter, r *http.Request) {
	var body securityChange
	if !s.decodeBody(w, r, &body) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.DiskAccess))
	if why := body.refusal(mode); why != "" {
		writeError(w, http.StatusBadRequest, why)
		return
	}
	if !s.needFile(w) {
		return
	}
	if err := s.saveSecurity(mode, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeSecurity(w)
}

// saveSecurity writes what the change names into the file and then hands it to every open
// conversation: the disk access when mode is set, each choice when the change carries it. The
// file takes all of it or none (a later key the environment overrides puts the earlier ones
// back), and no conversation hears of a change that did not stay.
func (s *Server) saveSecurity(mode string, change securityChange) error {
	if err := config.Transaction(s.opts.ConfigPath, func(tx config.Tx) error {
		return writeSecurityKeys(tx, mode, change)
	}); err != nil {
		return err
	}
	if mode != "" {
		for _, c := range s.snapshot() {
			applyDiskAccess(c.svc, mode)
		}
		s.info("the agent's disk access was changed from the settings", "disk_access", mode)
	}
	for _, t := range change.toggles() {
		if t.on == nil {
			continue
		}
		for _, c := range s.snapshot() {
			t.apply(c.svc, *t.on)
		}
		s.info(t.changed+" was changed from the settings", t.key, *t.on)
	}
	return nil
}

// toggle is one on/off choice of the page: the change's value (nil when it does not name it), what
// writes it into the file, what hands it to a conversation, and what the log says changed.
type toggle struct {
	on      *bool
	write   func(config.Tx, bool) error
	apply   func(Service, bool)
	changed string
	key     string
}

// toggles are the change's on/off choices, in the order they are written.
func (c securityChange) toggles() []toggle {
	return []toggle{
		{c.AskUnconfinedRuns, setAskUnconfinedRuns, applyAskUnconfinedRuns,
			"asking before unconfined runs", "ask_unconfined_runs"},
		{c.IsolateNetwork, setIsolateNetwork, applyIsolateNetwork,
			"the network isolation of the commands", "isolate_network"},
		{c.AutoApplySuggestions, setAutoApply, applyAutoApply,
			"applying suggestions without asking", "auto_apply_suggestions"},
		{c.DesktopControl, setDesktopControl, applyDesktopControl,
			"the agent's control of this computer's desktop", "desktop_control"},
	}
}

// writeSecurityKeys writes each key the change names into the file, stopping at the first that
// fails.
func writeSecurityKeys(tx config.Tx, mode string, change securityChange) error {
	if mode != "" {
		if err := setDiskAccess(tx, mode); err != nil {
			return err
		}
	}
	for _, t := range change.toggles() {
		if t.on == nil {
			continue
		}
		if err := t.write(tx, *t.on); err != nil {
			return err
		}
	}
	return nil
}

// startingDiskAccess hands a new conversation the disk access the file names now: the conversation
// was built from the configuration the gateway started with, and the settings may have changed it.
func (s *Server) startingDiskAccess(svc Service) {
	if s.opts.ConfigPath == "" {
		return
	}
	if cfg, err := loadConfig(s.opts.ConfigPath); err == nil {
		applyDiskAccess(svc, cfg.Sandbox.DiskAccess)
		applyAskUnconfinedRuns(svc, cfg.Sandbox.AskUnconfinedRuns)
		applyIsolateNetwork(svc, cfg.Sandbox.IsolateNetwork)
		applyAutoApply(svc, cfg.Agent.AutoApplySuggestions)
		applyDesktopControl(svc, cfg.Agent.DesktopControl)
	}
}

// applyDesktopControl gives svc the desktop control choice, when it can take one.
func applyDesktopControl(svc Service, on bool) {
	if ds, ok := svc.(desktopControlSetter); ok {
		ds.SetDesktopControl(on)
	}
}

// applyAutoApply gives svc the auto-apply choice, when it can take one.
func applyAutoApply(svc Service, on bool) {
	if as, ok := svc.(autoApplySetter); ok {
		as.SetAutoApplySuggestions(on)
	}
}

// applyIsolateNetwork gives svc the network isolation, when it can take one.
func applyIsolateNetwork(svc Service, on bool) {
	if ns, ok := svc.(networkIsolationSetter); ok {
		ns.SetIsolateNetwork(on)
	}
}

// applyAskUnconfinedRuns gives svc the unconfined-runs choice, when it can take one.
func applyAskUnconfinedRuns(svc Service, ask bool) {
	if us, ok := svc.(unconfinedRunsSetter); ok {
		us.SetAskUnconfinedRuns(ask)
	}
}

// applyDiskAccess gives svc the disk access, when it can take one.
func applyDiskAccess(svc Service, mode string) {
	if ds, ok := svc.(diskAccessSetter); ok {
		ds.SetDiskAccess(mode)
	}
}

// sessionScope is what GET /v1/sessions/{id}/scope answers: where the session's commands work and
// what the sandbox keeps from them, so the Security tab can show it for the session that is open.
type sessionScope struct {
	// Workspace is the folder the session works in: its worktree, or its own folder.
	Workspace string `json:"workspace"`
	// ProjectDir is the project's checkout, "" for a session of no project.
	ProjectDir string `json:"project_dir"`
	// Home is the HOME its commands get, "" when the sandbox keeps the old layout.
	Home string `json:"home"`
	// PrivateRoots are the folders its commands may not read beyond what they work in.
	PrivateRoots []string `json:"private_roots"`
}

// sandboxScoper is a service that can say what its sandbox gives a command.
type sandboxScoper interface {
	SandboxScope() (home string, private []string)
}

// handleSessionScope answers where a session works and what is kept from it.
func (s *Server) handleSessionScope(w http.ResponseWriter, r *http.Request) {
	c := conversationIfAny(r)
	c.stateMu.Lock()
	view := sessionScope{Workspace: c.workspace, ProjectDir: c.projectDir, PrivateRoots: []string{}}
	c.stateMu.Unlock()
	if sc, ok := c.svc.(sandboxScoper); ok {
		home, private := sc.SandboxScope()
		view.Home = home
		view.PrivateRoots = append(view.PrivateRoots, private...)
	}
	writeJSON(w, http.StatusOK, view)
}
