// Package doctor checks the machine motita runs on, for `motita doctor`.
//
// Each check is one line a user can act on: the platform the binary was built for against
// the one it runs on, the shell commands go through, where name servers come from and
// whether a name resolves, whether the binary is on the PATH, and the programs the
// interface calls. The checks that need the configuration (the file, the sandbox) are the
// caller's, because only the caller knows which file this run would read.
package doctor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/madkoding/motita/internal/dnsfix"
)

// Status is how a check came out.
type Status int

const (
	// OK means nothing to do.
	OK Status = iota
	// Warn means motita works, with something missing.
	Warn
	// Fail means something motita needs does not work.
	Fail
)

// Check is one line of the report.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Env is everything the checks read, so a test can describe any machine.
type Env struct {
	Version, Goos, Goarch string
	// Exe is the running binary's path, Shell the one commands go through.
	Exe, Shell string
	Getenv     func(string) string
	Stat       func(string) (os.FileInfo, error)
	LookPath   func(string) (string, error)
	// Resolve looks a host name up; DNS describes where name servers come from.
	Resolve func(ctx context.Context, host string) ([]string, error)
	DNS     func() string
	// Timeout bounds the name lookup.
	Timeout time.Duration
}

// System is the Env of this process.
func System(version, exe string) Env {
	return Env{
		Version:  version,
		Goos:     runtime.GOOS,
		Goarch:   runtime.GOARCH,
		Exe:      exe,
		Shell:    shellPath(),
		Getenv:   os.Getenv,
		Stat:     os.Stat,
		LookPath: exec.LookPath,
		// net.DefaultResolver is read at the call, so it is the one dnsfix.Install put in place.
		Resolve: func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		},
		DNS:     dnsfix.Describe,
		Timeout: 5 * time.Second,
	}
}

// probeHost is the name the DNS check resolves: the one every update and every sign-in
// needs, so a failure here is a failure the user would meet anyway.
const probeHost = "api.github.com"

// Environment runs the checks that need no configuration.
func Environment(e Env) []Check {
	checks := []Check{platform(e), shell(e), dns(e), onPath(e)}
	// Windows reads keys without stty; everywhere else the interface needs it.
	if e.Goos != "windows" {
		checks = append(checks, program(e, "stty", "the interface reads keys one at a time through it"))
	}
	return append(checks, program(e, "git", "the agent's git tools and the review need it"))
}

func onAndroid(e Env) bool {
	return e.Getenv("TERMUX_VERSION") != "" || e.Getenv("ANDROID_ROOT") != ""
}

func platform(e Env) Check {
	c := Check{Name: "platform", Detail: fmt.Sprintf("motita %s (%s/%s)", e.Version, e.Goos, e.Goarch)}
	if e.Goos == "linux" && onAndroid(e) {
		c.Status = Fail
		c.Detail += ": a linux build on Android dies with SIGSYS. Reinstall: curl -fsSL https://madkoding.github.io/motita/install.sh | sh"
	}
	return c
}

func shell(e Env) Check {
	c := Check{Name: "shell", Detail: e.Shell}
	info, err := e.Stat(e.Shell)
	switch {
	case err != nil:
		c.Status, c.Detail = Fail, e.Shell+" is missing: commands cannot run"
	// Windows has no execute bit: a file it can stat is one it can run.
	case e.Goos != "windows" && info.Mode()&0o111 == 0:
		c.Status, c.Detail = Fail, e.Shell+" is not executable: commands cannot run"
	}
	return c
}

func dns(e Env) Check {
	c := Check{Name: "dns", Detail: e.DNS()}
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()
	if _, err := e.Resolve(ctx, probeHost); err != nil {
		c.Status = Fail
		c.Detail += fmt.Sprintf("; %s does not resolve: %v", probeHost, err)
		return c
	}
	c.Detail += "; " + probeHost + " resolves"
	return c
}

func onPath(e Env) Check {
	dir := filepath.Dir(e.Exe)
	for _, d := range filepath.SplitList(e.Getenv("PATH")) {
		if filepath.Clean(d) == dir {
			return Check{Name: "path", Detail: dir + " is on the PATH"}
		}
	}
	return Check{Name: "path", Status: Warn, Detail: fmt.Sprintf("%s is not on the PATH: export PATH=\"%s:$PATH\"", dir, dir)}
}

func program(e Env, name, why string) Check {
	if p, err := e.LookPath(name); err == nil {
		return Check{Name: name, Detail: p}
	}
	return Check{Name: name, Status: Warn, Detail: "not found: " + why}
}

var marks = map[Status]string{OK: "✅", Warn: "⚠️ ", Fail: "❌"}

// Write prints the report and says whether any check failed.
func Write(w io.Writer, checks []Check) bool {
	failed, warned := false, false
	width := 0
	for _, c := range checks {
		width = max(width, len(c.Name))
	}
	for _, c := range checks {
		fmt.Fprintf(w, "%s %-*s  %s\n", marks[c.Status], width, c.Name, c.Detail)
		failed = failed || c.Status == Fail
		warned = warned || c.Status == Warn
	}
	switch {
	case failed:
		fmt.Fprintln(w, "\nSomething motita needs does not work: fix the ❌ lines above.")
	case warned:
		fmt.Fprintln(w, "\nmotita works; the ⚠️  lines above are worth a look.")
	default:
		fmt.Fprintln(w, "\nEverything motita needs works.")
	}
	return failed
}
