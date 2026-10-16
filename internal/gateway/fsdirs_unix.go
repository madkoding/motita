//go:build !windows

package gateway

// badNameChars cannot be in the name of a folder: a separator, or NUL.
const badNameChars = "/\\\x00"

// volumeRoot is the top of the tree a path lives in: the one root of the disk.
func volumeRoot(string) string { return "/" }

// platformDriveRoots are the drives of the machine; there is one tree here, so none.
func platformDriveRoots() []string { return nil }

// platformSystemTrees are where the system keeps its own files.
func platformSystemTrees() []string {
	return []string{
		"/etc", "/proc", "/sys", "/dev", "/boot", "/var", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/run",
		"/System", "/Library", "/private", "/cores",
	}
}

// sameName reports whether two names are the same folder name: exactly, on these systems.
func sameName(a, b string) bool { return a == b }
