package doctor

import "os"

// shellPath is the interpreter the agent runs actions through on Windows (see
// internal/agent): ComSpec, or cmd.exe where it lives by default.
func shellPath() string {
	if s := os.Getenv("ComSpec"); s != "" {
		return s
	}
	return `C:\Windows\System32\cmd.exe`
}
