//go:build !linux

package sandbox

import "fmt"

// Write confinement is only implemented on Linux (Landlock).
func landlockABI() int { return 0 }

func confineWrites(roots []string) error {
	return fmt.Errorf("write confinement is only implemented on Linux")
}

func gitDirs(dir string) []string { return nil }
