//go:build windows

package sandbox

// Windows has no /bin/echo: the command processor is the portable way to produce
// output, and it is what a Windows user would type anyway.

import "os"

func echoCommand(text string) (string, []string) {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	return shell, []string{"/c", "echo " + text}
}

func failCommand() (string, []string) {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	return shell, []string{"/c", "exit 1"}
}

func sleepCommand(seconds int) (string, []string) {
	// ping is the portable "wait" on Windows and it is present everywhere.
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	return shell, []string{"/c", "ping -n " + itoa(seconds+1) + " 127.0.0.1 >NUL"}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
