package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HOW MUCH OF THE DISK THE AGENT REACHES.
//
// One choice, asked in the settings page's Security tab: the workspace alone, or the whole disk.
//
//   - workspace (the default): write_file and edit_file refuse a path outside the working
//     directory, and on Linux (Landlock) a command can neither write outside it nor read the
//     user's files outside it - the home directory, /home, /root, /mnt, /media, /srv.
//   - full: both reach anywhere the user's account can. motita's own logins and keys stay
//     unreadable to a command either way.
//
// A command the user approves one by one is not confined in either mode: that approval is the
// person deciding, for that command, what the setting decides for the rest.

// The disk access modes.
const (
	DiskWorkspace = "workspace"
	DiskFull      = "full"
)

// ValidDiskAccess reports whether mode is one sandbox.disk_access accepts.
func ValidDiskAccess(mode string) bool {
	return mode == DiskWorkspace || mode == DiskFull
}

// FullDisk reports whether the agent may reach the whole disk. Anything but "full" is the
// workspace: a value that did not load is never read as the wider one.
func (s Sandbox) FullDisk() bool { return s.DiskAccess == DiskFull }

// SetDiskAccess writes mode into the configuration file at path as sandbox.disk_access, IN PLACE.
//
// Like SetGatewayExposure it edits one line rather than re-marshalling, so the file's comments
// survive. The file is then loaded whole, and a change the environment overrides
// (MOTITA_SANDBOX_DISK_ACCESS) is undone and refused: saving a choice that does not apply would
// tell the person the agent is confined when it is not, or the reverse.
func SetDiskAccess(path, mode string) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return Tx{path}.SetDiskAccess(mode)
}

// SetDiskAccess is SetDiskAccess inside a transaction.
func (t Tx) SetDiskAccess(mode string) error {
	if !ValidDiskAccess(mode) {
		return fmt.Errorf("the disk access %q is not one of workspace, full", mode)
	}
	return setSandboxKey(t.path, "disk_access", strconv.Quote(mode),
		func(cfg Config) bool { return cfg.Sandbox.DiskAccess == mode })
}

// SetAskUnconfinedRuns writes ask into the configuration file at path as
// sandbox.ask_unconfined_runs, in place and with the same checks as SetDiskAccess.
func SetAskUnconfinedRuns(path string, ask bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return Tx{path}.SetAskUnconfinedRuns(ask)
}

// SetAskUnconfinedRuns is SetAskUnconfinedRuns inside a transaction.
func (t Tx) SetAskUnconfinedRuns(ask bool) error {
	return setSandboxKey(t.path, "ask_unconfined_runs", strconv.FormatBool(ask),
		func(cfg Config) bool { return cfg.Sandbox.AskUnconfinedRuns == ask })
}

// SetIsolateNetwork writes on into the configuration file at path as sandbox.isolate_network, in
// place and with the same checks as SetDiskAccess.
func SetIsolateNetwork(path string, on bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return Tx{path}.SetIsolateNetwork(on)
}

// SetIsolateNetwork is SetIsolateNetwork inside a transaction.
func (t Tx) SetIsolateNetwork(on bool) error {
	return setSandboxKey(t.path, "isolate_network", strconv.FormatBool(on),
		func(cfg Config) bool { return cfg.Sandbox.IsolateNetwork == on })
}

// SetAutoApplySuggestions writes on into the configuration file at path as
// agent.auto_apply_suggestions, in place and with the same checks as SetDiskAccess.
func SetAutoApplySuggestions(path string, on bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return Tx{path}.SetAutoApplySuggestions(on)
}

// SetAutoApplySuggestions is SetAutoApplySuggestions inside a transaction.
func (t Tx) SetAutoApplySuggestions(on bool) error {
	return setBlockKey(t.path, "agent", "auto_apply_suggestions", strconv.FormatBool(on),
		func(cfg Config) bool { return cfg.Agent.AutoApplySuggestions == on })
}

// SetApproveAllCommands writes on into the configuration file at path as agent.approve_all_commands,
// in place and with the same checks as SetDiskAccess.
func SetApproveAllCommands(path string, on bool) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return Tx{path}.SetApproveAllCommands(on)
}

// SetApproveAllCommands is SetApproveAllCommands inside a transaction.
func (t Tx) SetApproveAllCommands(on bool) error {
	return setBlockKey(t.path, "agent", "approve_all_commands", strconv.FormatBool(on),
		func(cfg Config) bool { return cfg.Agent.ApproveAllCommands == on })
}

// SetApprovalTimeout is agent.approval_timeout inside a transaction, with the same checks as
// SetDiskAccess.
func (t Tx) SetApprovalTimeout(d time.Duration) error {
	return setBlockKey(t.path, "agent", "approval_timeout", d.String(),
		func(cfg Config) bool { return cfg.Agent.ApprovalTimeout == d })
}

// SetDesktopControl is agent.desktop_control inside a transaction, with the same checks as
// SetDiskAccess.
func (t Tx) SetDesktopControl(on bool) error {
	return setBlockKey(t.path, "agent", "desktop_control", strconv.FormatBool(on),
		func(cfg Config) bool { return cfg.Agent.DesktopControl == on })
}

// setSandboxKey sets sandbox.key; see setBlockKey.
func setSandboxKey(path, key, value string, applied func(Config) bool) error {
	return setBlockKey(path, "sandbox", key, value, applied)
}

// setBlockKey, with configWriteMu held by the caller, sets block.key to value (YAML text) in the
// file at path, then loads the file whole and undoes the change when it does not load or when
// applied says the loaded configuration does not carry it: the environment
// (MOTITA_<BLOCK>_<KEY>) overrides the file.
func setBlockKey(path, block, key, value string, applied func(Config) bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setBlockKeyText(string(body), block, key, value)
	if err != nil {
		return err
	}
	perm := info.Mode().Perm()
	if err := writeConfigAtomic(path, []byte(text), perm); err != nil {
		return err
	}
	cfg, err := LoadWithoutKey(path)
	if err == nil && !applied(cfg) {
		env := "MOTITA_" + strings.ToUpper(block) + "_" + strings.ToUpper(key)
		err = fmt.Errorf("%s in the gateway's environment overrides %s.%s: "+
			"remove it from the environment, or change it there", env, block, key)
	}
	if err != nil {
		if rerr := writeConfigAtomic(path, body, perm); rerr != nil {
			return fmt.Errorf("%w (and the file could not be restored: %v)", err, rerr)
		}
		return err
	}
	return nil
}
