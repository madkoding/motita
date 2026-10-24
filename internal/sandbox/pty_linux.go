package sandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"syscall"
)

// ptyHooks are the system calls a pseudo-terminal is made of. They are variables so a test can
// make each one fail; the production ones are the plain syscalls.
var ptyHooks = struct {
	open  func(path string, flag int, perm os.FileMode) (*os.File, error)
	ioctl func(fd, req uintptr, arg []byte) syscall.Errno
}{
	open:  os.OpenFile,
	ioctl: ptyIoctl,
}

// ptyIoctl runs an ioctl whose argument is the struct in arg, and copies back what the kernel wrote
// into it. The kernel is pointed at an anonymous mapping, not at a Go variable, so no unsafe pointer
// is needed (see kernelBuffer).
func ptyIoctl(fd, req uintptr, arg []byte) syscall.Errno {
	buf, err := mmapAnon(-1, 0, len(arg), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return syscall.ENOMEM
	}
	defer func() { _ = syscall.Munmap(buf) }()
	copy(buf, arg)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, reflect.ValueOf(&buf[0]).Pointer())
	copy(arg, buf)
	return errno
}

// openPTY opens a pseudo-terminal: the master the gateway reads and writes, and the slave the
// shell gets as its terminal. It is what posix_openpt, unlockpt and ptsname do, with no C library:
// /dev/ptmx hands out a new pair, TIOCSPTLCK unlocks its slave and TIOCGPTN names it.
func openPTY() (*os.File, *os.File, error) {
	master, err := ptyHooks.open("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open a pseudo-terminal: %w", err)
	}
	// Both arguments are a C int: 0 unlocks, and the number comes back in it.
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCSPTLCK, make([]byte, 4)); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("could not unlock the pseudo-terminal: %w", errno)
	}
	n := make([]byte, 4)
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCGPTN, n); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("could not name the pseudo-terminal: %w", errno)
	}
	name := "/dev/pts/" + strconv.FormatUint(uint64(binary.NativeEndian.Uint32(n)), 10)
	slave, err := ptyHooks.open(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("could not open the pseudo-terminal's other end: %w", err)
	}
	return master, slave, nil
}

// resizePTY tells the terminal behind master its new size; the shell gets a SIGWINCH.
func resizePTY(master *os.File, cols, rows int) error {
	// struct winsize of <sys/ioctl.h>: rows, cols, xpixel, ypixel, each an unsigned short.
	ws := make([]byte, 8)
	binary.NativeEndian.PutUint16(ws[0:], clampSize(rows))
	binary.NativeEndian.PutUint16(ws[2:], clampSize(cols))
	if errno := ptyHooks.ioctl(master.Fd(), syscall.TIOCSWINSZ, ws); errno != 0 {
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
