package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// ptyHooks are the system calls a pseudo-terminal is made of. They are variables so a test can
// make each one fail; the production ones are the plain syscalls.
var ptyHooks = struct {
	open  func(path string, flag int, perm os.FileMode) (*os.File, error)
	ioctl func(fd, req, arg uintptr) syscall.Errno
}{
	open: os.OpenFile,
	ioctl: func(fd, req, arg uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
		return errno
	},
}

// openPTY opens a pseudo-terminal: the master the gateway reads and writes, and the slave the
// shell gets as its terminal. It is what posix_openpt, unlockpt and ptsname do, with no C library:
// /dev/ptmx hands out a new pair, TIOCSPTLCK unlocks its slave and TIOCGPTN names it.
func openPTY() (*os.File, *os.File, error) {
	master, err := ptyHooks.open("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open a pseudo-terminal: %w", err)
	}
	var unlock int32
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("could not unlock the pseudo-terminal: %w", errno)
	}
	var n uint32
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("could not name the pseudo-terminal: %w", errno)
	}
	slave, err := ptyHooks.open("/dev/pts/"+strconv.FormatUint(uint64(n), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("could not open the pseudo-terminal's other end: %w", err)
	}
	return master, slave, nil
}

// winsize is struct winsize of <sys/ioctl.h>.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// resizePTY tells the terminal behind master its new size; the shell gets a SIGWINCH.
func resizePTY(master *os.File, cols, rows int) error {
	ws := winsize{rows: clampSize(rows), cols: clampSize(cols)}
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return fmt.Errorf("could not resize the terminal: %w", errno)
	}
	return nil
}

// terminalAttributes makes the isolation child the leader of a session of its own, with the
// terminal on its standard input as the controlling one: what a shell needs for job control and
// Ctrl-C. A session leader is the leader of its process group too, which is what the group kill
// (killGroup) reaches, so the group the attributes asked for is given by the session instead.
func terminalAttributes(attr *syscall.SysProcAttr) *syscall.SysProcAttr {
	attr.Setpgid = false
	attr.Setsid = true
	attr.Setctty = true
	attr.Ctty = 0
	return attr
}
