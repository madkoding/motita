//go:build !windows

package onboard

// credentialsProtection describes what was really done to keep the key file
// private. On Unix the file mode is enforced by the kernel, so the honest answer is
// the mode that was set.
func credentialsProtection() string {
	return "permissions 0600, keep it out of the repository"
}
