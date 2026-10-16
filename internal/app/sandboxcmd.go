package app

import (
	"fmt"

	"github.com/madkoding/motita/internal/sandbox"
)

// runSandboxCommand is `motita sandbox apparmor <print|install|remove>`: the profile that lets this
// binary make the user namespaces a command without a network runs in, on a system that takes
// them from every other program. print shows it, install and remove (which need root) change
// /etc/apparmor.d. Like the other subcommands it builds neither an engine nor a sandbox.
func (op Options) runSandboxCommand(action string) int {
	switch action {
	case "print":
		profile, err := sandbox.AppArmorProfile(op.ExePath)
		if err != nil {
			return op.sandboxFailed(err)
		}
		fmt.Fprint(op.Out, profile)
	case "install":
		if err := op.InstallAppArmor(op.ExePath); err != nil {
			return op.sandboxFailed(err)
		}
		fmt.Fprintf(op.Out, "✅ profile for %s loaded from %s: commands can now run without a network.\n"+
			"   Restart the gateway or the interface to use it.\n", op.ExePath, sandbox.AppArmorProfilePath)
	default:
		if err := op.RemoveAppArmor(); err != nil {
			return op.sandboxFailed(err)
		}
		fmt.Fprintln(op.Out, "✅ profile removed.")
	}
	return Success
}

// sandboxFailed reports why the profile could not be printed, installed or removed.
func (op Options) sandboxFailed(err error) int {
	fmt.Fprintf(op.Err, "❌ %v\n", err)
	return RunError
}
