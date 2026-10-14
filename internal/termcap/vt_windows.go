//go:build windows

package termcap

import (
	"os"
	"syscall"
)

// The console flag that makes conhost interpret escape sequences instead of printing them. It is
// not in the syscall package, and this module takes no dependencies, so it is spelled here.
const enableVirtualTerminalProcessing = 0x0004

var procSetConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// enableVT asks the console behind f to interpret escape sequences, and reports whether it does.
//
// Windows Terminal already has the flag on; the classic console window that powershell.exe and
// cmd.exe open does not, and prints every colour code as text until a program asks. Windows older
// than 10 refuses the flag, and the caller then draws without escapes.
func enableVT(f *os.File) bool {
	if f == nil {
		return false
	}
	h := syscall.Handle(f.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	ok, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return ok != 0
}
