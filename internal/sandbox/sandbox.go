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
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/gpgagent"
	"github.com/madkoding/motita/internal/logx"
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
	ModeWriteConfine Mode = "write_confined"
	ModeReadConfine  Mode = "read_confined"
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
	// ToolsDir is a persistent directory OUTSIDE every repository where the agent's
	// toolchains, its HOME and its temporary directories live. Empty keeps the old
	// layout (HOME and TMPDIR inside the working directory). See tools.go.
	ToolsDir string
	// ConfineWrites restricts what a command can WRITE to the working directory, the HOME in the
	// tools directory, its own temporary directory and the git directories a worktree needs,
	// whatever the command does (Linux, Landlock). A request marked Unconfined, one the user
	// approved, is exempt. Reads are restricted only for HiddenPaths.
	ConfineWrites bool
	// GitAuthDir is the directory of the git logins and GitHome the user's real home. They
	// give the git the agent runs what the user's terminal has: the user's own git
	// configuration and the logins motita holds, neither of which is visible from the
	// sandbox's HOME. Both empty leaves the environment as it was.
	GitAuthDir string
	GitHome    string
	// GPG is the user's gpg-agent a command may sign with (see gpgagent.Resolve): its socket and
	// the public half of the keyring become readable and GNUPGHOME points at them. Zero grants
	// nothing. The private keys stay hidden either way.
	GPG gpgagent.Access
	// HiddenPaths are files and directories a confined command may not read: motita's logins and
	// stored keys, which no command needs. Like the write confinement, it spares what the user
	// approved, which also keeps motita's logins for git.
	HiddenPaths []string
	// Secrets are the variables the person keeps for the agent (NAME to value), read at every
	// command so a change applies at once. Each reaches the command as an environment variable,
	// and its value is masked in the output the command prints.
	Secrets func() map[string]string
	// FullDisk lifts the confinement to the workspace: a command may then write anywhere and read
	// everything but HiddenPaths. Without it (the default) a confined command also cannot read the
	// user's files outside the directories it works in (see privatePaths). SetFullDisk changes it
	// while the sandbox lives.
	FullDisk bool
	// Home is the agent's HOME inside the tools directory, relative to it; empty is "home". A
	// session that belongs to a project gets one of its own (see ProjectHome), so what one
	// project's tools leave in HOME - logins, caches, shell history - is not another project's.
	Home string
	// PrivateRoots are more trees a command confined to the workspace may not read, apart from the
	// directories it works in: the gateway's workspace root, which holds every other project and
	// session, wherever it is on the disk.
	PrivateRoots []string
	Log          *logx.Logger
}

// Sandbox runs commands in a controlled environment.
type Sandbox struct {
	op         Options
	log        *logx.Logger
	cg         *cgroup
	base       string
	executable string
	notApplied []string
	// gitRoots are the git directories the base worktree, and the worktrees of the project's other
	// repositories inside it, write outside it; repoRoots are the repositories they belong to. Both
	// are read once here, before any command could rewrite a `.git` file.
	gitRoots  []string
	repoRoots []string
	// fullDisk is Options.FullDisk as it is now. It is a pointer so the copies WithDir makes for
	// background agents follow a change made to the sandbox they came from.
	fullDisk *atomic.Bool
	// noNetwork is whether the commands run without a network, shared with the copies WithDir
	// makes like fullDisk. Options.Limits.NoNetwork is only the request New starts from; each
	// run's own is runLimits'.
	noNetwork *atomic.Bool
}

// noRunGroups is set once a child could not be started inside a run's cgroup. What refused it
// is the kernel or the machine's permissions, the same for every sandbox of this process.
var noRunGroups atomic.Bool

// New prepares the sandbox and detects which isolation is really available.
func New(op Options) (*Sandbox, error) {
	if op.Log == nil {
		op.Log = logx.Global()
	}
	// The workspace directory is only required when a chroot root is set, because
	// that is the only isolation mechanism that enters a different directory.
	// When there is no chroot the process keeps its own working directory and the
	// configured path is not entered — it is only recorded as metadata.
	if op.UseChroot {
		if err := createWorkDir(&op); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(op.Dir)
	if err != nil {
		return nil, fmt.Errorf("could not resolve the working directory %q: %w", op.Dir, err)
	}
	selfExecutable, err := osHooks.executable()
	if err != nil {
		return nil, fmt.Errorf("could not locate this very executable (needed for the isolation): %w", err)
	}

	s := &Sandbox{op: op, log: op.Log, base: abs, executable: selfExecutable,
		fullDisk: new(atomic.Bool), noNetwork: new(atomic.Bool)}
	s.fullDisk.Store(op.FullDisk)
	s.op.Limits.NoNetwork = false
	s.gitRoots, s.repoRoots = worktreeGit(abs)

	// Asked BEFORE this process stops being dumpable: a process that is not root can only map a
	// user namespace for a child while it is (see needsNamespaceRelay). The answer is kept.
	_ = networkIsolationError()
	if err := hideFromChildren(); err != nil {
		s.notApplied = append(s.notApplied, "process hiding: a command can read motita's environment in /proc: "+err.Error())
	}
	// The isolation this platform cannot give any run (on Linux there is none). It is recorded
	// here and not per run: runs are concurrent, and the list is read while they go on.
	_, warnings := childAttributes(s.op.Limits, false, 0, 0)
	s.notApplied = append(s.notApplied, warnings...)

	if op.UseChroot {
		s.checkChroot()
	}

	if op.UseCgroups {
		s.joinCgroup()
	}

	if op.ConfineWrites {
		s.checkWriteConfinement()
	}

	if op.Limits.NoNetwork {
		s.requestNetwork(true)
	}

	if !hasLimits(op.Limits) {
		s.log.Debug("no POSIX limits configured: only the ephemeral directory is isolated")
	}

	// An RLIMIT_AS below the address space the isolation process already uses
	// would abort the Go runtime itself (fatal error: runtime: cannot allocate
	// memory). It is adjusted here, in the parent, and it is logged: the child
	// cannot warn without contaminating the command's output.
	if s.op.Limits.MemoryMB > 0 {
		s.raiseMemoryLimit()
	}

	s.recordUnappliedLimits()

	s.log.Info("sandbox ready",
		"directory", s.base,
		"chroot", s.op.UseChroot,
		"cgroups", s.cg != nil,
		"limits", hasLimits(op.Limits),
		"not_applied", strings.Join(s.notApplied, " | "))
	return s, nil
}

// createWorkDir creates the working directory a chroot enters ("." when none is set).
func createWorkDir(op *Options) error {
	if op.Dir == "" {
		op.Dir = "."
	}
	if err := os.MkdirAll(op.Dir, 0o755); err != nil {
		return fmt.Errorf("could not create the working directory %q: %w", op.Dir, err)
	}
	return nil
}

// checkWriteConfinement drops the requested write confinement, recording why, when there is no
// chroot to confine the writes and the kernel has no Landlock.
func (s *Sandbox) checkWriteConfinement() {
	if !s.op.UseChroot && landlockABI() == 0 {
		s.notApplied = append(s.notApplied, "write confinement: this kernel has no Landlock, so a command can "+
			"still write outside the working directory")
		s.op.ConfineWrites = false
	}
}

// recordUnappliedLimits records the limits the shell refuses: such a limit used to be dropped in
// silence, the command running as if it were bounded.
func (s *Sandbox) recordUnappliedLimits() {
	unapplied, err := unappliedLimits(s.op.Limits)
	if err != nil {
		s.notApplied = append(s.notApplied, "limits: could not check that the shell applies them: "+err.Error())
	}
	for _, name := range unapplied {
		s.notApplied = append(s.notApplied, name+": the shell cannot apply this limit here, so commands run without it")
		s.log.Warn("a sandbox limit cannot be applied", "limit", name)
	}
}

// checkChroot drops a requested chroot, recording why, when the process is not root or the
// root directory is not there.
func (s *Sandbox) checkChroot() {
	if osHooks.euid() != 0 {
		s.notApplied = append(s.notApplied, "chroot: it was requested but the process is not root")
		s.op.UseChroot = false
		return
	}
	if _, err := os.Stat(s.op.Root); err != nil {
		s.notApplied = append(s.notApplied, fmt.Sprintf("chroot: the root %q is not accessible: %v", s.op.Root, err))
		s.op.UseChroot = false
	}
}

// joinCgroup creates the cgroup and adds the main process to it, so that its children inherit
// the membership even if the re-exec does not do it explicitly. A failure is recorded and the
// sandbox goes on with the POSIX limits only.
func (s *Sandbox) joinCgroup() {
	cg, err := newCgroup(s.op.CgroupRoot, s.op.Limits)
	if err != nil {
		s.notApplied = append(s.notApplied, "cgroups v1: "+err.Error())
		s.log.Warn("cgroups v1 not available: only POSIX limits are applied", "error", err)
		return
	}
	s.cg = cg
	if err := s.cg.addProcess(os.Getpid()); err != nil {
		s.notApplied = append(s.notApplied, "cgroups v1: could not add the process to the group: "+err.Error())
		s.log.Warn("could not add the process to the cgroup", "error", err)
	}
}

// raiseMemoryLimit lifts a memory limit below the address space the isolation process already
// uses, and records it.
func (s *Sandbox) raiseMemoryLimit() {
	if minimum := minimumMemoryMB(); minimum > 0 && s.op.Limits.MemoryMB < minimum {
		s.notApplied = append(s.notApplied, fmt.Sprintf(
			"memory_mb: applying %d MB instead of %d because the process launching the sandbox "+
				"already uses that address space",
			minimum, s.op.Limits.MemoryMB))
		s.log.Warn("raising the sandbox memory limit",
			"requested_mb", s.op.Limits.MemoryMB, "applied_mb", minimum)
		s.op.Limits.MemoryMB = minimum
	}
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
	if s.IsolatesNetwork() {
		modes = append(modes, ModeNoNetwork)
	}
	if s.op.ConfineWrites && !s.op.UseChroot && !s.FullDisk() {
		modes = append(modes, ModeWriteConfine, ModeReadConfine)
	}
	return modes
}

// SetFullDisk lets the commands run from now on reach the whole disk (true) or confines them to
// the directories they work in (false). A command already running keeps the rules it started with.
func (s *Sandbox) SetFullDisk(full bool) {
	if s.fullDisk == nil {
		// A Sandbox built by hand rather than by New: it gets its switch now.
		s.fullDisk = new(atomic.Bool)
	}
	s.fullDisk.Store(full)
}

// FullDisk reports whether the commands this sandbox runs reach the whole disk. A Sandbox built by
// hand rather than by New is confined to the workspace, the default.
func (s *Sandbox) FullDisk() bool { return s.fullDisk != nil && s.fullDisk.Load() }

// ConfinementAvailable reports whether this kernel can confine what a command reads and writes
// (Linux with Landlock). Where it cannot, the workspace limit holds for motita's own file actions
// only.
func ConfinementAvailable() bool { return landlockABI() > 0 }

// NotApplied lists the isolation that was requested and could not be applied.
func (s *Sandbox) NotApplied() []string { return s.notApplied }

// Base is the absolute working directory the sandbox was built around. It is what
// every relative path handed to the child is resolved against.
func (s *Sandbox) Base() string { return s.base }

// Run runs an isolated command. It returns the combined output, whether it was
// truncated, the exit code and an error only when the command could not be run.
func (s *Sandbox) Run(ctx context.Context, p execx.Request) (string, bool, int, error) {
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

	run, err := s.prepare(p, nil)
	if err != nil {
		return "", false, -1, err
	}
	defer run.cleanup()

	out, truncated, code, err := s.launch(ctx, s.executable, run.args, run.workDir, timeout, maxOutput, run.limits)
	out = s.maskSecrets(out)
	if code != 0 {
		out = s.withFailureHint(out, run.confined, run.tempDir)
	}
	return out, truncated, code, err
}

// preparedRun is a command made ready for the isolation child: the arguments that hand it its
// spec, where it runs, and the temporary directory cleanup removes once it is over.
type preparedRun struct {
	args     []string
	workDir  string
	tempDir  string
	limits   Limits
	confined bool
	cleanup  func()
}

// prepare makes p ready to run in the isolation child, with extraEnv set over the environment
// every command gets (a terminal's TERM, say).
func (s *Sandbox) prepare(p execx.Request, extraEnv []string) (preparedRun, error) {
	// An empty command is rejected here: without this check it reached the
	// child process, which returned code 126 without explaining that the
	// problem was that there was nothing to run.
	if strings.TrimSpace(p.Command) == "" {
		return preparedRun{}, fmt.Errorf("cannot run an empty command in the sandbox")
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
		return preparedRun{}, fmt.Errorf("could not prepare the working directory %q: %w", workDir, err)
	}
	// Resolve to an absolute path HERE, while the process is still in the
	// directory the configuration was relative to. The child re-executes this
	// binary and chdirs to this value: a relative one would be resolved inside
	// the child's own starting directory, which is not the same place, and the
	// commands would then run somewhere the validator cannot see their effect.
	// (Measured on a real i386 machine: with workspace_dir ./workspace the anchor
	// failed every check while the identical configuration with an absolute path
	// passed.)
	absWorkDir, absErr := filepath.Abs(workDir)
	if absErr != nil {
		return preparedRun{}, fmt.Errorf("could not resolve the working directory %q: %w", p.Dir, absErr)
	}
	workDir = absWorkDir

	// TMPDIR ephemeral and private to this run. It is created under the tools directory
	// when there is one: inside the working directory every run left a tmp-* folder in
	// the repository, in git status and in front of gofmt.
	tempDir, err := os.MkdirTemp(s.tempRoot(), "tmp-*")
	if err != nil {
		return preparedRun{}, fmt.Errorf("could not create the temporary directory: %w", err)
	}
	cleanup := func() {
		if s.op.Keep {
			return
		}
		if err := removeTree(tempDir); err != nil {
			s.log.Warn("could not delete the temporary directory", "dir", tempDir, "error", err)
		}
	}

	confine := s.op.ConfineWrites && !s.op.UseChroot && !p.Unconfined
	spec := Spec{
		Command:     p.Command,
		Args:        append([]string(nil), p.Args...),
		Dir:         workDir,
		Limits:      s.runLimits(p),
		Environment: withVariables(s.environmentWithTmp(tempDir, !confine || p.Approved, p.SystemToolsFirst), append(s.secretEnv(), extraEnv...)),
	}

	switch {
	case confine && p.Approved:
		s.confineApproved(&spec)
	case confine:
		s.confine(&spec, workDir, tempDir)
	default:
		// Not confined: the command writes wherever the sandbox's other rules let it.
	}

	spec.DropPrivileges = s.op.DropPrivs
	spec.Uid, spec.Gid = s.op.Uid, s.op.Gid
	if s.op.UseChroot {
		// Inside the chroot the real command is the path relative to the root.
		spec.Chroot = s.op.Root
		spec.ChrootDir = "/" + strings.TrimPrefix(strings.TrimPrefix(workDir, s.base), "/")

		inside := filepath.Join(spec.Chroot, p.Command)
		if _, err := os.Stat(inside); err != nil {
			cleanup()
			return preparedRun{}, fmt.Errorf("the command %q does not exist inside the chroot %q: %w", p.Command, inside, err)
		}
	}

	encoded, err := spec.Encode()
	if err != nil {
		cleanup()
		return preparedRun{}, err
	}

	s.log.Debug("running in sandbox",
		"command", p.Command,
		"work_dir", workDir,
		"temp_dir", tempDir,
		"isolation", strings.Join(modesToStrings(s.Isolation()), ","))

	return preparedRun{
		args:     []string{ChildMarker, encoded},
		workDir:  workDir,
		tempDir:  tempDir,
		limits:   spec.Limits,
		confined: confine && !p.Approved,
		cleanup:  cleanup,
	}, nil
}

// withVariables is env with each NAME=value of extra set over the variable of that name.
func withVariables(env, extra []string) []string {
	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		env = slices.DeleteFunc(env, func(e string) bool { return strings.HasPrefix(e, name+"=") })
		env = append(env, kv)
	}
	return env
}

// withFailureHint appends to the output of a failed command the hint that fits it: how to sign
// with gpg, or where a confined command can put temporary files.
func (s *Sandbox) withFailureHint(out string, confinedRun bool, tempDir string) string {
	if s.op.GPG.Enabled() && gpgagent.FailedToSign(out) {
		// A sandboxed command has no terminal for pinentry: say what to do instead of leaving git's
		// one line to be guessed at.
		return strings.TrimRight(out, "\n") + "\n" + gpgagent.Hint + "\n"
	}
	if confinedRun && refusedSystemTemp(out) {
		return strings.TrimRight(out, "\n") + "\n" + fmt.Sprintf(tempHint, tempDir) + "\n"
	}
	return out
}

// tempHint says where a confined command can put temporary files. A model's habit is /tmp, which
// is shared with every other program of the machine and so is not among the write roots: the kernel
// refuses the write and the model, seeing only "Permission denied", spends rounds on TMPDIR and
// chmod instead of changing the path.
const tempHint = "motita: /tmp is not writable from this sandbox. Write temporary files under $TMPDIR (%s, " +
	"private to this command and deleted when it ends) or, to keep them between commands, under $MOTITA_SCRATCH."

// refusedSystemTemp reports whether out shows a write under /tmp that the kernel refused.
func refusedSystemTemp(out string) bool {
	if !strings.Contains(out, "/tmp/") {
		return false
	}
	lower := strings.ToLower(out)
	return strings.Contains(lower, "permission denied") || strings.Contains(lower, "read-only file system") ||
		strings.Contains(lower, "operation not permitted")
}

// sharedMemory is where POSIX shared memory and named semaphores live. /dev itself is granted only
// the files already there (`> /dev/null`), which left Python's multiprocessing (a Lock, a process
// pool, pytest-xdist) and headless Chromium failing with "Permission denied" even for a command the
// user approved. A missing directory (macOS) is skipped when the rules are made.
const sharedMemory = "/dev/shm"

// writeRoots are the directories a confined command may write: where it works, the home the
// sandbox keeps for it, its own temporary directory, the git directories a worktree writes into,
// /dev for `> /dev/null` and sharedMemory.
//
// The tools directory's bin/ and tools/ are not among them. They come first on the PATH of every
// session of every project, so a command one project's agent ran unasked could otherwise plant a
// `git` or a `make` there for all the others. Installing a toolchain is a command the user
// approves, and an approved command is not confined.
func (s *Sandbox) writeRoots(workDir, tempDir string) []string {
	roots := []string{s.base, workDir, tempDir, "/dev", sharedMemory}
	if home := s.Home(); home != "" {
		roots = append(roots, home)
	}
	roots = append(roots, s.gitRoots...)
	if workDir != s.base {
		writes, _ := worktreeGit(workDir)
		roots = append(roots, writes...)
	}
	return roots
}

// confine fills in what a confined command may reach. On the whole disk it may write anywhere: the
// one root is "/", and only motita's logins and keys stay out of reach. Confined to the workspace it
// writes only under writeRoots and reads the system, the directories it writes and readRoots, never
// the user's files elsewhere (privatePaths).
func (s *Sandbox) confine(spec *Spec, workDir, tempDir string) {
	if s.FullDisk() {
		spec.WriteRoots = []string{"/", sharedMemory}
		spec.HiddenPaths = s.hiddenPaths(spec.WriteRoots)
		// With the whole disk writable, the secrets above are kept unreadable AND unwritable (a
		// command could otherwise rewrite motita.yaml to disable the policy), and the tools bin
		// directories are kept unwritable so a PATH hijack cannot be planted. See landlock_linux.go.
		spec.ReadonlyPaths = s.readonlyToolPaths()
		return
	}
	spec.WriteRoots = s.writeRoots(workDir, tempDir)
	spec.HiddenPaths = s.hiddenPaths(spec.WriteRoots)
	spec.ReadRoots = s.readRoots(workDir)
	spec.PrivatePaths = s.privatePaths()
}

// readonlyToolPaths are the tools directory's bin directories: <tools>/bin and <tools>/tools,
// where the toolchains the agent installs live and which come first on every session's PATH. They
// stay readable (a command must be able to run what is installed) but, with the whole disk
// writable, are kept unwritable, so a command cannot plant a program there to hijack a later one.
// No tools directory (the old layout, or a chroot) means none.
func (s *Sandbox) readonlyToolPaths() []string {
	tools := s.toolsDir()
	if tools == "" {
		return nil
	}
	return []string{filepath.Join(tools, "bin"), filepath.Join(tools, "tools")}
}

// privateTrees are where a machine keeps people's files: none of it is the workspace's business.
var privateTrees = []string{"/home", "/root", "/mnt", "/media", "/srv", "/run/media"}

// confineApproved fills in what a command the user approved by name may reach: it writes anywhere,
// as the user said it may, and reads everything but motita's configuration, its stored keys and the
// model logins. The git logins stay readable: an approved `git push` is what they are for, and the
// credential helper git starts runs inside the same confinement.
func (s *Sandbox) confineApproved(spec *Spec) {
	spec.WriteRoots = []string{"/", sharedMemory}
	spec.HiddenPaths = s.approvedHidden()
}

// approvedHidden are the paths an approved command still may not read: the hidden paths it was
// given, with the logins directory replaced by the logins in it that are not git's.
func (s *Sandbox) approvedHidden() []string {
	auth := ""
	if strings.TrimSpace(s.op.GitAuthDir) != "" {
		auth = filepath.Clean(s.op.GitAuthDir)
	}
	var hidden []string
	for _, h := range s.op.HiddenPaths {
		if auth == "" || filepath.Clean(h) != auth {
			hidden = append(hidden, h)
			continue
		}
		entries, _ := os.ReadDir(auth)
		for _, e := range entries {
			if !gitforge.IsLoginFile(e.Name()) {
				hidden = append(hidden, filepath.Join(auth, e.Name()))
			}
		}
	}
	return hidden
}

// privatePaths are the trees a command confined to the workspace may not read: privateTrees and
// the platform's own (platformPrivateTrees: the profiles on Windows), the user's own home wherever
// it is, the PrivateRoots it was given and the tools directory, which holds the HOME and the
// worktrees of every other project. The directories the command works in
// stay readable even when they are inside one of them: they are granted by name (writeRoots,
// readRoots), and so are the toolchains.
func (s *Sandbox) privatePaths() []string {
	private := append(append([]string(nil), privateTrees...), platformPrivateTrees()...)
	for _, p := range append([]string{s.op.GitHome, s.toolsDir()}, s.op.PrivateRoots...) {
		if p != "" && filepath.IsAbs(p) && filepath.Clean(p) != "/" {
			private = append(private, filepath.Clean(p))
		}
	}
	return private
}

// readRoots are what a command confined to the workspace may read besides the system and the
// directories it writes: the toolchains in the tools directory (not the rest of it, see
// privatePaths), the user's git configuration (its files, not the credential store beside them),
// and the repository each worktree belongs to: git reads its config, HEAD and packed refs on every
// command, and its checkout holds the dependency directories linked into the worktree.
func (s *Sandbox) readRoots(workDir string) []string {
	var roots []string
	if tools := s.toolsDir(); tools != "" {
		roots = append(roots, filepath.Join(tools, "bin"), filepath.Join(tools, "tools"))
	}
	var gitConfigDirs []string
	if s.op.GitHome != "" {
		roots = append(roots, filepath.Join(s.op.GitHome, ".gitconfig"))
		gitConfigDirs = append(gitConfigDirs, filepath.Join(s.op.GitHome, ".config", "git"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		gitConfigDirs = append(gitConfigDirs, filepath.Join(xdg, "git"))
	}
	for _, dir := range gitConfigDirs {
		for _, name := range []string{"config", "ignore", "attributes"} {
			roots = append(roots, filepath.Join(dir, name))
		}
	}
	if s.op.GPG.Enabled() {
		roots = append(roots, gpgagent.PublicPaths(s.op.GPG.Home)...)
		roots = append(roots, s.op.GPG.Socket)
	}
	roots = append(roots, s.repoRoots...)
	if workDir != s.base {
		_, repos := worktreeGit(workDir)
		roots = append(roots, repos...)
	}
	return roots
}

// worktreeGit are the git directories a command in dir writes outside it (see gitDirs) and the
// repositories they belong to, for dir and for the worktrees of the project's other repositories
// inside it. A repository is its checkout when its git directory is the checkout's `.git`.
func worktreeGit(dir string) (writes, repos []string) {
	trees := []string{dir}
	// A repository is a folder of the project like any other; the ones added before that lived under
	// gitx.ReposDir. gitDirs says which of them are worktrees.
	for _, parent := range []string{dir, filepath.Join(dir, gitx.ReposDir)} {
		entries, _ := os.ReadDir(parent)
		for _, e := range entries {
			if e.IsDir() {
				trees = append(trees, filepath.Join(parent, e.Name()))
			}
		}
	}
	for _, tree := range trees {
		dirs := gitDirs(tree)
		if len(dirs) == 0 {
			continue
		}
		writes = append(writes, dirs...)
		// The first is the worktree's own directory, <repository>/worktrees/<id>.
		repo := filepath.Dir(filepath.Dir(dirs[0]))
		if filepath.Base(repo) == ".git" {
			repo = filepath.Dir(repo)
		}
		repos = append(repos, repo)
	}
	return writes, repos
}

// HiddenPaths is a copy of the paths this sandbox was given to hide, so a sandbox rebuilt for
// another directory hides the same files: the configuration a run was loaded from among them.
func (s *Sandbox) HiddenPaths() []string {
	return append([]string(nil), s.op.HiddenPaths...)
}

// hiddenPaths are the paths a confined command may not read: the ones it was given, and the
// user's git credential stores, which hold the same kind of token as motita's logins. Left out is
// any that holds a directory the command writes, which would leave it writing where it cannot
// read, and any inside one: the command could move it to a readable name, and a file in the
// workspace is the project's own.
func (s *Sandbox) hiddenPaths(roots []string) []string {
	candidates := append([]string(nil), s.op.HiddenPaths...)
	if s.op.GitHome != "" {
		candidates = append(candidates, filepath.Join(s.op.GitHome, ".git-credentials"),
			filepath.Join(s.op.GitHome, ".config", "git", "credentials"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "git", "credentials"))
	}
	if home := s.gpgHome(); home != "" {
		candidates = append(candidates, gpgagent.PrivatePaths(home)...)
	}
	var hidden []string
	for _, h := range candidates {
		h = filepath.Clean(h)
		skip := !filepath.IsAbs(h)
		for _, root := range roots {
			root = filepath.Clean(root)
			skip = skip || strings.HasPrefix(root+"/", h+"/") || strings.HasPrefix(h, root+"/")
		}
		if !skip {
			hidden = append(hidden, h)
		}
	}
	return hidden
}

// gpgHome is the GnuPG home whose private keys no confined command may read: the one granted for
// signing, else the user's own, whether or not signing is on.
func (s *Sandbox) gpgHome() string {
	if s.op.GPG.Enabled() {
		return s.op.GPG.Home
	}
	return gpgagent.Home(s.op.GitHome)
}

// launchCommand builds the command that the isolation child will run. It is a
// variable so a test can observe the working directory it is given without
// launching a process.
var launchCommand = func(ctx context.Context, command string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, command, args...)
}

// launch starts the isolation child and collects its output.
//
// limits are the run's own (runLimits): whether it gets a network is decided per request.
func (s *Sandbox) launch(ctx context.Context, command string, args []string, dir string, timeout time.Duration,
	maxOutput int64, limits Limits) (string, bool, int, error) {
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The platform's warnings were recorded once, in New.
	attr, _ := childAttributes(limits, false, 0, 0)
	out := &limitedBuffer{max: maxOutput}

	start := time.Now()
	group := s.newRunGroup()
	cmd := s.childCommand(childCtx, command, args, dir, group.attach(attr), group, out)
	err := cmd.Start()
	if err != nil && group != nil {
		// The kernel would not start the child inside the group (no clone3, or no right to move
		// a process there). It runs without one, and later runs do not try again.
		noRunGroups.Store(true)
		s.log.Debug("runs are not contained by a cgroup", "error", err)
		_ = group.remove()
		group = nil
		cmd = s.childCommand(childCtx, command, args, dir, attr, nil, out)
		err = cmd.Start()
	}
	if err == nil {
		err = cmd.Wait()
	}
	// What the command left running in the background (`cmd &`, or detached with setsid) goes
	// with it, not only on a timeout: it would otherwise outlive the run and its limits' purpose,
	// and keep its temporary directory busy after it is deleted.
	killLeftovers(cmd)
	group.kill()
	_ = group.remove()
	duration := time.Since(start)
	text := out.buf.String()

	if childCtx.Err() == context.DeadlineExceeded {
		return text, out.truncated, -1, fmt.Errorf("the command exceeded the %s limit in the sandbox", timeout)
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The command exited 0, and only a child it left behind held the output open past
		// WaitDelay. That child is gone now, and the run succeeded.
		err = nil
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
				return text, out.truncated, exit, fmt.Errorf(
					"the process was killed by a signal (exit=%d): check the sandbox memory and CPU limits", exit)
			}
			return text, out.truncated, exit, nil
		}
		return text, out.truncated, -1, fmt.Errorf("could not start the isolation: %w", err)
	}
	s.log.Debug("sandbox run finished", "exit", exit, "duration_ms", duration.Milliseconds(), "truncated", out.truncated)
	return text, out.truncated, exit, nil
}

// childCommand is the isolation child of one run, writing to out, started in group when it is
// not nil (attr then carries it).
func (s *Sandbox) childCommand(ctx context.Context, command string, args []string, dir string,
	attr *syscall.SysProcAttr, group *runGroup, out *limitedBuffer) *exec.Cmd {
	cmd := launchCommand(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = s.environment()
	if attr != nil {
		cmd.SysProcAttr = attr
	}
	cmd.Cancel = func() error {
		group.kill()
		return killGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd
}

// newRunGroup is the cgroup the next run starts in, or nil: none can be made here, or starting
// a child in one already failed.
func (s *Sandbox) newRunGroup() *runGroup {
	if noRunGroups.Load() {
		return nil
	}
	return newRunGroup()
}

// environmentWithTmp returns the child's environment: bounded, without the
// agent's secrets and with TMPDIR pointing at the ephemeral temporary directory
// of this run.
//
// logins says whether git is given motita's logins (its credential helper and
// where they live). Only a command the user approved, or one that is not
// confined at all, gets them: the helper hands a token to whoever asks, and a
// confined command is one nobody looked at.
func (s *Sandbox) environmentWithTmp(tempDir string, logins, systemFirst bool) []string {
	path, home := s.commandPath(), s.base
	if tools := s.toolsDir(); tools != "" {
		if systemFirst {
			path = ToolPathLast(tools, path)
		} else {
			path = ToolPath(tools, path)
		}
		home = s.Home()
	}
	base := []string{
		"PATH=" + path,
		"HOME=" + home,
		"LANG=C.UTF-8",
		"TMPDIR=" + tempDir,
	}
	if goos == "windows" {
		base = append(base, windowsEnvironment(tempDir, home)...)
	}
	if scratch := s.scratchDir(); scratch != "" {
		base = append(base, "MOTITA_SCRATCH="+scratch)
	}
	for _, key := range []string{"TERM", "USER", "SHELL"} {
		if v, ok := os.LookupEnv(key); ok {
			base = append(base, key+"="+v)
		}
	}
	if s.op.GitAuthDir != "" || s.op.GitHome != "" {
		exe, auth := s.executable, s.op.GitAuthDir
		if !logins {
			exe, auth = "", ""
		}
		base = append(base, gitforge.GitEnv(exe, auth, s.op.GitHome)...)
	}
	if view := s.gpgView(); view != "" {
		base = append(base, "GNUPGHOME="+view)
	}
	return base
}

// windowsSystemVariables are what a Windows program needs from the system to start at all: without
// SystemRoot PowerShell does not load ("Loading managed Windows PowerShell failed with error
// 8009001d"), without PATHEXT nothing is found by its bare name, and without ComSpec a tool cannot
// reach cmd.exe. They name the system, not the user, and are passed through as motita has them.
var windowsSystemVariables = []string{
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "OS", "NUMBER_OF_PROCESSORS",
	"PROCESSOR_ARCHITECTURE", "ProgramData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432",
	"CommonProgramFiles", "CommonProgramFiles(x86)", "CommonProgramW6432", "USERNAME", "COMPUTERNAME",
}

// windowsEnvironment is what a command on Windows gets besides PATH, HOME and TMPDIR: the system
// variables above, the temporary directory under the names Windows programs read (TEMP and TMP,
// not TMPDIR), and the profile folders in the sandbox's HOME, as HOME already is on every system.
func windowsEnvironment(tempDir, home string) []string {
	env := []string{
		"TEMP=" + tempDir,
		"TMP=" + tempDir,
		"USERPROFILE=" + home,
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
	}
	for _, key := range windowsSystemVariables {
		if v := getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// environment is the environment without a temporary directory of its own (for
// the isolation process, which does not need TMPDIR nor git's logins).
func (s *Sandbox) environment() []string {
	return s.environmentWithTmp(s.base, false, false)
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

// Readable reports whether a command of this sandbox may read path, by the same rule confine gives
// Landlock: on the whole disk everything but the hidden paths; confined to the workspace, the
// directories it works in and readRoots, and the system outside privatePaths. It is what motita's
// own read tools (the ones that do not run as a command) answer by, so a file the agent cannot
// `cat` is not one it can read some other way. path is absolute and already resolved.
func (s *Sandbox) Readable(path string) bool {
	path = filepath.Clean(path)
	// No run is in progress, so there is no temporary directory of its own to count: the system's
	// would grant everything under /tmp, a workspace root kept private among it.
	writes := s.writeRoots(s.base, s.base)
	if s.FullDisk() {
		writes = []string{"/"}
	}
	if pathUnder(path, s.hiddenPaths(writes)) {
		return false
	}
	if s.FullDisk() || pathUnder(path, writes) || pathUnder(path, s.readRoots(s.base)) {
		return true
	}
	return !pathUnder(path, s.privatePaths())
}

// pathUnder reports whether p is one of targets or inside one.
func pathUnder(p string, targets []string) bool {
	p = foldCase(p)
	for _, t := range targets {
		t = foldCase(filepath.Clean(t))
		if p == t || strings.HasPrefix(p, strings.TrimSuffix(t, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
