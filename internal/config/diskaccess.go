package config

import (
	"fmt"
	"os"
	"strconv"
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
	if !ValidDiskAccess(mode) {
		return fmt.Errorf("the disk access %q is not one of workspace, full", mode)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setBlockKeyText(string(body), "sandbox", "disk_access", strconv.Quote(mode))
	if err != nil {
		return err
	}
	perm := info.Mode().Perm()
	if err := writeConfigAtomic(path, []byte(text), perm); err != nil {
		return err
	}
	cfg, err := LoadWithoutKey(path)
	if err == nil && cfg.Sandbox.DiskAccess != mode {
		err = fmt.Errorf("MOTITA_SANDBOX_DISK_ACCESS in the gateway's environment overrides sandbox.disk_access: " +
			"remove it from the environment, or change it there")
	}
	if err != nil {
		if rerr := writeConfigAtomic(path, body, perm); rerr != nil {
			return fmt.Errorf("%w (and the file could not be restored: %v)", err, rerr)
		}
		return err
	}
	return nil
}
