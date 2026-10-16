//go:build windows

package gateway

import (
	"os"
	"path/filepath"
)

// badNameChars cannot be in the name of a folder on Windows.
const badNameChars = "/\\\x00<>:\"|?*"

// volumeRoot is the top of the drive (or network share) a path lives in, such as C:\.
func volumeRoot(path string) string { return filepath.VolumeName(path) + `\` }

// platformDriveRoots are the drives that exist, such as C:\ and D:\.
func platformDriveRoots() []string {
	var out []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			out = append(out, root)
		}
	}
	return out
}

// platformSystemTrees are where Windows keeps its own files and the installed programs.
func platformSystemTrees() []string {
	var out []string
	for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if dir := os.Getenv(env); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}
