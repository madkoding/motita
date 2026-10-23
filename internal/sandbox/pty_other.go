//go:build !linux

package sandbox

import (
	"errors"
	"os"
	"syscall"
)

// errNoPTY is why a terminal cannot be opened here: only Linux has the pseudo-terminal this
// package knows how to open without a C library.
var errNoPTY = errors.New("an interactive terminal is only available on Linux for now")

func openPTY() (*os.File, *os.File, error) { return nil, nil, errNoPTY }

func resizePTY(*os.File, int, int) error { return errNoPTY }

func terminalAttributes(attr *syscall.SysProcAttr) *syscall.SysProcAttr { return attr }
