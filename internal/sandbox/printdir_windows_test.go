//go:build windows

package sandbox

import "os"

// printWorkingDirectory returns the command and arguments that print the current
// directory, so a test can assert where the command really ran.
func printWorkingDirectory() (string, []string) {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	// "cd" with no arguments prints the current directory on Windows.
	return shell, []string{"/c", "cd"}
}
