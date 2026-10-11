//go:build linux

// Command seccomptrap runs a program under the one seccomp rule that matters on Android:
// faccessat2 is trapped, so the process gets SIGSYS and dies, the way an Android app's
// seccomp policy does it. CI uses it to prove the android build survives that policy and
// that a linux build does not (which is what keeps the check honest).
//
//	seccomptrap <program> [args...]
//
// It is a test harness for CI and ships in no release.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"syscall"
)

const (
	prSetNoNewPrivs   = 38
	prSetSeccomp      = 22
	seccompModeFilter = 2

	// The generic syscall table numbers faccessat2 439 on every 64-bit architecture.
	sysFaccessat2 = 439

	bpfLdWAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfRetK   = 0x06 // BPF_RET | BPF_K
	retTrap   = 0x00030000
	retAllow  = 0x7fff0000
)

// filter is the BPF program: load the syscall number, trap faccessat2, allow the rest.
func filter() []byte {
	type insn struct {
		code   uint16
		jt, jf uint8
		k      uint32
	}
	prog := []insn{
		{bpfLdWAbs, 0, 0, 0}, // seccomp_data.nr
		{bpfJeqK, 0, 1, sysFaccessat2},
		{bpfRetK, 0, 0, retTrap},
		{bpfRetK, 0, 0, retAllow},
	}
	out := make([]byte, 0, 8*len(prog))
	for _, i := range prog {
		out = binary.LittleEndian.AppendUint16(out, i.code)
		out = append(out, i.jt, i.jf)
		out = binary.LittleEndian.AppendUint32(out, i.k)
	}
	return out
}

// kernelBuffer copies src into an anonymous mapping, whose address is stable and outside the
// Go heap, so the kernel can be pointed at it without the unsafe package (the same route as
// the sandbox's Landlock code).
func kernelBuffer(src []byte) uintptr {
	buf, err := syscall.Mmap(-1, 0, len(src), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		fail("mmap: %v", err)
	}
	copy(buf, src)
	return reflect.ValueOf(&buf[0]).Pointer()
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "seccomptrap: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: seccomptrap <program> [args...]")
	}
	// Both prctl calls and the exec happen on this one thread: a seccomp filter belongs to
	// the thread that installs it, and execve hands the caller's filter to the new program.
	runtime.LockOSThread()

	code := filter()
	// struct sock_fprog on a 64-bit system: u16 len, 6 bytes of padding, a pointer.
	fprog := binary.LittleEndian.AppendUint16(nil, uint16(len(code)/8))
	fprog = append(fprog, 0, 0, 0, 0, 0, 0)
	fprog = binary.LittleEndian.AppendUint64(fprog, uint64(kernelBuffer(code)))

	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		fail("PR_SET_NO_NEW_PRIVS: %v", errno)
	}
	_, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetSeccomp, seccompModeFilter, kernelBuffer(fprog))
	if errno != 0 {
		fail("PR_SET_SECCOMP: %v", errno)
	}
	if err := syscall.Exec(os.Args[1], os.Args[1:], os.Environ()); err != nil {
		fail("exec %s: %v", os.Args[1], err)
	}
}
