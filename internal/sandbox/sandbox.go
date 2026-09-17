// Package sandbox implements Layer C: lightweight isolated execution, without
// Docker.
//
// Isolation is applied in layers, according to what the system allows:
//
//  1. An ephemeral directory per attempt (always). It is deleted when the run
//     finishes unless keeping it is requested.
//  2. setrlimit in the child: CPU, memory (address space), processes, file
//     descriptors and maximum file size. Requires no privileges.
//  3. cgroups v1 for memory and PIDs, if they exist and there is write
//     permission. A memory setrlimit is an approximation (RLIMIT_AS bounds the
//     address space, not the resident set); cgroups gives the real bound.
//  4. chroot + privilege drop, only if the process is root and it was
//     requested.
//  5. A network namespace of its own (CLONE_NEWNET) if isolating the network is
//     requested.
//
// The setrlimits and the chroot are applied by a child process which is this
// same binary re-executed (see child.go): that way there is no dependency on
// util-linux or cgo, and no intermediate Go process is left consuming the
// limited budget.
//
// Everything that could NOT be applied is recorded as a warning: a sandbox that
// does not isolate but says it isolates is worse than having no sandbox.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// osHooks are the operating-system operations of the parent process whose
// failure cannot be provoked with a plain filesystem setup: there is no portable
// way of making os.Executable fail on a healthy machine and a process cannot
// stop being root on demand. They are variables holding the real functions (the
// same injectable-seam pattern as childHooks) so that those error paths are
// tested instead of assumed. Production always calls the real ones.
var osHooks = struct {
	executable func() (string, error)
	euid       func() int
}{
	executable: os.Executable,
	euid:       os.Geteuid,
}

// Mode of isolation effectively applied.
type Mode string

const (
	ModeEphemeral    Mode = "ephemeral"
	ModeLimits       Mode = "posix_limits"
	ModeCgroups      Mode = "cgroups_v1"
	ModeChroot       Mode = "chroot"
	ModeNoNetwork    Mode = "netns_no_network"
	ModeUnprivileged Mode = "unprivileged"
)

// Options for building the sandbox.
type Options struct {
	Dir         string // base working directory
	Limits      Limits
	UseCgroups  bool
	CgroupRoot  string
	UseChroot   bool
	Root        string
	Uid, Gid    int
	DropPrivs   bool
	Keep        bool
	Timeout     time.Duration
	MaxOutputKB int
	Log         *logx.Logger
}

// Sandbox runs commands in a controlled environment.
type Sandbox struct {
	op         Options
	log        *logx.Logger
	cg         *cgroup
	base       string
	executable string
	notApplied []string
}

// New prepares the sandbox and detects which isolation is really available.
func New(op Options) (*Sandbox, error) {
	if op.Log == nil {
		op.Log = logx.Global()
	}
	if op.Dir == "" {
		op.Dir = "."
	}
	if err := os.MkdirAll(op.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create the working directory %q: %w", op.Dir, err)
	}
	abs, err := filepath.Abs(op.Dir)
	if err != nil {
		return nil, fmt.Errorf("could not resolve the working directory %q: %w", op.Dir, err)
	}
	selfExecutable, err := osHooks.executable()
	if err != nil {
		return nil, fmt.Errorf("could not locate this very executable (needed for the isolation): %w", err)
	}

	s := &Sandbox{op: op, log: op.Log, base: abs, executable: selfExecutable}

	if op.UseChroot {
		switch {
		case osHooks.euid() != 0:
			s.notApplied = append(s.notApplied, "chroot: it was requested but the process is not root")
			s.op.UseChroot = false
		default:
			if _, err := os.Stat(op.Root); err != nil {
				s.notApplied = append(s.notApplied, fmt.Sprintf("chroot: the root %q is not accessible: %v", op.Root, err))
				s.op.UseChroot = false
			}
		}
	}

	if op.UseCgroups {
		cg, err := newCgroup(op.CgroupRoot, op.Limits)
		if err != nil {
			s.notApplied = append(s.notApplied, "cgroups v1: "+err.Error())
			s.log.Warn("cgroups v1 not available: only POSIX limits are applied", "error", err)
		} else {
			s.cg = cg
		}
	}

	// The main process joins the cgroup so that its children inherit the
	// membership even if the re-exec does not do it explicitly.
	if s.cg != nil {
		if err := s.cg.addProcess(os.Getpid()); err != nil {
			s.notApplied = append(s.notApplied, "cgroups v1: could not add the process to the group: "+err.Error())
			s.log.Warn("could not add the process to the cgroup", "error", err)
		}
	}

	if !hasLimits(op.Limits) {
		s.log.Debug("no POSIX limits configured: only the ephemeral directory is isolated")
	}

	// An RLIMIT_AS below the address space the isolation process already uses
	// would abort the Go runtime itself (fatal error: runtime: cannot allocate
	// memory). It is adjusted here, in the parent, and it is logged: the child
	// cannot warn without contaminating the command's output.
	if s.op.Limits.MemoryMB > 0 {
		if minimum := minimumMemoryMB(); minimum > 0 && s.op.Limits.MemoryMB < minimum {
			s.notApplied = append(s.notApplied, fmt.Sprintf(
				"memory_mb: applying %d MB instead of %d because the process launching the sandbox already uses that address space",
				minimum, s.op.Limits.MemoryMB))
			s.log.Warn("raising the sandbox memory limit",
				"requested_mb", s.op.Limits.MemoryMB, "applied_mb", minimum)
			s.op.Limits.MemoryMB = minimum
		}
	}

	s.log.Info("sandbox ready",
		"directory", s.base,
		"chroot", s.op.UseChroot,
		"cgroups", s.cg != nil,
		"limits", hasLimits(op.Limits),
		"not_applied", strings.Join(s.notApplied, " | "))
	return s, nil
}

// Close releases the cgroup group if it was created.
func (s *Sandbox) Close() error {
	if s.cg == nil {
		return nil
	}
	if err := s.cg.remove(); err != nil {
		s.log.Warn("could not remove the cgroup", "error", err)
		return err
	}
	return nil
}

// Isolation returns a description of what is really applied.
func (s *Sandbox) Isolation() []Mode {
	modes := []Mode{ModeEphemeral}
	if hasLimits(s.op.Limits) {
		modes = append(modes, ModeLimits)
	}
	if s.cg != nil {
		modes = append(modes, ModeCgroups)
	}
	if s.op.UseChroot {
		modes = append(modes, ModeChroot)
	}
	if s.op.DropPrivs {
		modes = append(modes, ModeUnprivileged)
	}
	if s.op.Limits.NoNetwork {
		modes = append(modes, ModeNoNetwork)
	}
	return modes
}

// NotApplied lists the isolation that was requested and could not be applied.
func (s *Sandbox) NotApplied() []string { return s.notApplied }

// Run runs an isolated command. It returns the combined output, whether it was
// truncated, the exit code and an error only when the command could not be run.
func (s *Sandbox) Run(ctx context.Context, p execx.Request) (string, bool, int, error) {
	// An empty command is rejected here: without this check it reached the
	// child process, which returned code 126 without explaining that the
	// problem was that there was nothing to run.
	if strings.TrimSpace(p.Command) == "" {
		return "", false, -1, fmt.Errorf("cannot run an empty command in the sandbox")
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = s.op.Timeout
	}
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	maxOutput := p.MaxOutput
	if maxOutput <= 0 {
		maxOutput = int64(s.op.MaxOutputKB) << 10
	}
	if maxOutput <= 0 {
		maxOutput = 256 << 10
	}

	// Working directory: the one the caller asks for, or the base one.
	//
	// Important: an ephemeral directory is NOT used as the working directory.
	// The effect of the work (the generated file, the commit, the report) has to
	// be visible afterwards for the anchor and for the final action; if actions
	// ran in a directory that is deleted when the run finishes, the validator
	// could never see the result and the agent would fail every time. The
	// ephemeral isolation applies to TMPDIR, which is where temporary files go.
	workDir := p.Dir
	if workDir == "" {
		workDir = s.base
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", false, -1, fmt.Errorf("could not prepare the working directory %q: %w", workDir, err)
	}

	// TMPDIR ephemeral and private to this run.
	tempDir, err := os.MkdirTemp(s.base, "tmp-*")
	if err != nil {
		return "", false, -1, fmt.Errorf("could not create the temporary directory: %w", err)
	}
	if !s.op.Keep {
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				s.log.Warn("could not delete the temporary directory", "dir", tempDir, "error", err)
			}
		}()
	}

	spec := Spec{
		Command:     p.Command,
		Args:        append([]string(nil), p.Args...),
		Dir:         workDir,
		Limits:      s.op.Limits,
		Environment: s.environmentWithTmp(tempDir),
	}

	command := s.executable
	var args []string

	if s.op.UseChroot {
		// Inside the chroot the real command is the path relative to the root.
		spec.Chroot = s.op.Root
		spec.ChrootDir = "/" + strings.TrimPrefix(strings.TrimPrefix(workDir, s.base), "/")
		spec.DropPrivileges = s.op.DropPrivs
		spec.Uid, spec.Gid = s.op.Uid, s.op.Gid

		inside := filepath.Join(spec.Chroot, p.Command)
		if _, err := os.Stat(inside); err != nil {
			return "", false, -1, fmt.Errorf("the command %q does not exist inside the chroot %q: %w", p.Command, inside, err)
		}
	} else {
		spec.DropPrivileges = s.op.DropPrivs
		spec.Uid, spec.Gid = s.op.Uid, s.op.Gid
	}

	encoded, err := spec.Encode()
	if err != nil {
		return "", false, -1, err
	}
	args = []string{ChildMarker, encoded}

	s.log.Debug("running in sandbox",
		"command", p.Command,
		"work_dir", workDir,
		"temp_dir", tempDir,
		"isolation", strings.Join(modesToStrings(s.Isolation()), ","))

	return s.launch(ctx, command, args, workDir, timeout, maxOutput)
}

// launch starts the isolation child and collects its output.
func (s *Sandbox) launch(ctx context.Context, command string, args []string, dir string, timeout time.Duration, maxOutput int64) (string, bool, int, error) {
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(childCtx, command, args...)
	cmd.Dir = dir
	cmd.Env = s.environment()

	attr, warnings := childAttributes(s.op.Limits, false, 0, 0)
	if attr != nil {
		cmd.SysProcAttr = attr
	}
	// The platform's warnings (the isolation it could not apply) are recorded
	// like the rest. On Linux childAttributes has none; on the platforms where
	// it has, this keeps them.
	s.notApplied = append(s.notApplied, warnings...)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second

	out := &limitedBuffer{max: maxOutput}
	cmd.Stdout = out
	cmd.Stderr = out

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	text := out.buf.String()

	if childCtx.Err() == context.DeadlineExceeded {
		return text, out.truncated, -1, fmt.Errorf("the command exceeded the %s limit in the sandbox", timeout)
	}

	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
			if exit == 127 {
				return text, out.truncated, exit, fmt.Errorf("the command could not be run inside the sandbox (code 127)")
			}
			if exit < 0 {
				return text, out.truncated, exit, fmt.Errorf("the process was killed by a signal (exit=%d): check the sandbox memory and CPU limits", exit)
			}
			return text, out.truncated, exit, nil
		}
		return text, out.truncated, -1, fmt.Errorf("could not start the isolation: %w", err)
	}
	s.log.Debug("sandbox run finished", "exit", exit, "duration_ms", duration.Milliseconds(), "truncated", out.truncated)
	return text, out.truncated, exit, nil
}

// environmentWithTmp returns the child's environment: bounded, without the
// agent's secrets and with TMPDIR pointing at the ephemeral temporary directory
// of this run.
func (s *Sandbox) environmentWithTmp(tempDir string) []string {
	base := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + s.base,
		"LANG=C.UTF-8",
		"TMPDIR=" + tempDir,
	}
	for _, key := range []string{"TERM", "USER", "SHELL"} {
		if v, ok := os.LookupEnv(key); ok {
			base = append(base, key+"="+v)
		}
	}
	return base
}

// environment is the environment without a temporary directory of its own (for
// the isolation process, which does not need TMPDIR).
func (s *Sandbox) environment() []string {
	return s.environmentWithTmp(s.base)
}

// limitedBuffer accumulates up to max bytes and flags whether there was a cut.
type limitedBuffer struct {
	buf       strings.Builder
	max       int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	space := b.max - int64(b.buf.Len())
	if space <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if space < int64(len(p)) {
		b.buf.Write(p[:space])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func modesToStrings(modes []Mode) []string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		out = append(out, string(m))
	}
	return out
}

// IsolationJSON exposes the applied isolation for the logs.
func (s *Sandbox) IsolationJSON() string {
	data, err := jsonMarshal(map[string]any{
		"modes":       modesToStrings(s.Isolation()),
		"not_applied": s.notApplied,
		"directory":   s.base,
	})
	if err != nil {
		return `{"modes":[],"not_applied":["not serialisable"]}`
	}
	return string(data)
}
