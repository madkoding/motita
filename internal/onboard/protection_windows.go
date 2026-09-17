//go:build windows

package onboard

// credentialsProtection describes what was really done to keep the key file
// private. os.Chmod on Windows only toggles the read-only bit: access is decided by
// the ACLs, which this program does not set, so claiming "0600" would be a promise
// it cannot keep.
func credentialsProtection() string {
	return "keep it private: on Windows the file permissions come from the ACLs, not from this tool"
}
