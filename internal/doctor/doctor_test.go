package doctor

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// healthy is a machine where every check passes.
func healthy(t *testing.T) Env {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": "/usr/bin:" + filepath.Dir(exe)}
	return Env{
		Version: "v1", Goos: "linux", Goarch: "arm64", Exe: exe, Shell: shellPath(),
		Getenv:   func(k string) string { return env[k] },
		Stat:     func(string) (os.FileInfo, error) { return os.Stat(exe) },
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Resolve:  func(context.Context, string) ([]string, error) { return []string{"1.2.3.4"}, nil },
		DNS:      func() string { return "/etc/resolv.conf" },
		Timeout:  time.Second,
	}
}

func byName(checks []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range checks {
		m[c.Name] = c
	}
	return m
}

func TestAHealthyMachinePassesEveryCheck(t *testing.T) {
	var out bytes.Buffer
	if Write(&out, Environment(healthy(t))) {
		t.Fatalf("a healthy machine failed:\n%s", out.String())
	}
	for _, want := range []string{"motita v1 (linux/arm64)", shellPath(), "api.github.com resolves", "is on the PATH", "/usr/bin/stty", "Everything motita needs works."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report misses %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "❌") || strings.Contains(out.String(), "⚠") {
		t.Errorf("a healthy report carries a warning:\n%s", out.String())
	}
}

func TestALinuxBuildOnAndroidFails(t *testing.T) {
	for _, v := range []string{"TERMUX_VERSION", "ANDROID_ROOT"} {
		e := healthy(t)
		e.Getenv = func(k string) string {
			if k == v {
				return "x"
			}
			return ""
		}
		c := byName(Environment(e))["platform"]
		if c.Status != Fail || !strings.Contains(c.Detail, "SIGSYS") || !strings.Contains(c.Detail, "install.sh") {
			t.Errorf("%s: %+v", v, c)
		}
		e.Goos = "android"
		if c := byName(Environment(e))["platform"]; c.Status != OK {
			t.Errorf("%s: the android build was flagged: %+v", v, c)
		}
	}
}

func TestAMissingOrNonExecutableShellFails(t *testing.T) {
	e := healthy(t)
	e.Stat = func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
	if c := byName(Environment(e))["shell"]; c.Status != Fail || !strings.Contains(c.Detail, "missing") {
		t.Errorf("missing shell: %+v", c)
	}
	plain := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.Stat = func(string) (os.FileInfo, error) { return os.Stat(plain) }
	if c := byName(Environment(e))["shell"]; c.Status != Fail || !strings.Contains(c.Detail, "not executable") {
		t.Errorf("non-executable shell: %+v", c)
	}
}

func TestANameThatDoesNotResolveFails(t *testing.T) {
	e := healthy(t)
	e.Resolve = func(context.Context, string) ([]string, error) { return nil, errors.New("no servers") }
	c := byName(Environment(e))["dns"]
	if c.Status != Fail || !strings.Contains(c.Detail, "no servers") || !strings.HasPrefix(c.Detail, "/etc/resolv.conf") {
		t.Errorf("%+v", c)
	}
}

func TestABinaryOffThePathAndMissingProgramsWarn(t *testing.T) {
	e := healthy(t)
	e.Getenv = func(k string) string {
		if k == "PATH" {
			return "/nowhere"
		}
		return ""
	}
	e.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	checks := byName(Environment(e))
	if c := checks["path"]; c.Status != Warn || !strings.Contains(c.Detail, "export PATH=") {
		t.Errorf("path: %+v", c)
	}
	for _, name := range []string{"stty", "git"} {
		if c := checks[name]; c.Status != Warn || !strings.HasPrefix(c.Detail, "not found") {
			t.Errorf("%s: %+v", name, c)
		}
	}
	var out bytes.Buffer
	if Write(&out, Environment(e)) {
		t.Errorf("warnings alone failed the report:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "worth a look") {
		t.Errorf("the summary does not point at the warnings:\n%s", out.String())
	}
}

func TestWriteSaysWhenSomethingFailed(t *testing.T) {
	var out bytes.Buffer
	if !Write(&out, []Check{{Name: "x", Status: Fail, Detail: "broken"}}) {
		t.Fatal("a failed check did not fail the report")
	}
	if !strings.Contains(out.String(), "❌ x  broken") || !strings.Contains(out.String(), "fix the ❌ lines") {
		t.Errorf("%s", out.String())
	}
}

func TestSystemDescribesThisProcess(t *testing.T) {
	e := System("v2", "/bin/motita")
	if e.Version != "v2" || e.Exe != "/bin/motita" || e.Goos == "" || e.Goarch == "" || e.Timeout <= 0 {
		t.Fatalf("%+v", e)
	}
	if e.DNS() == "" || e.Getenv("PATH") != os.Getenv("PATH") {
		t.Error("System does not read this process")
	}
	if e.Shell != shellPath() {
		t.Errorf("Shell = %q, want %q", e.Shell, shellPath())
	}
	if _, err := e.Stat(shellPath()); err != nil {
		t.Errorf("stat %s: %v", shellPath(), err)
	}
	if _, err := e.LookPath("sh"); err != nil {
		t.Errorf("lookpath sh: %v", err)
	}
	// A cancelled lookup returns at once on any machine, with or without a network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Resolve(ctx, "localhost.invalid"); err == nil {
		t.Error("a cancelled lookup succeeded")
	}
}

func TestWindowsSkipsTheExecuteBitAndStty(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "cmd.exe")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := healthy(t)
	e.Goos, e.Shell = "windows", plain
	e.Stat = os.Stat
	checks := byName(Environment(e))
	if c := checks["shell"]; c.Status != OK {
		t.Errorf("windows shell: %+v", c)
	}
	if _, ok := checks["stty"]; ok {
		t.Error("windows was asked for stty")
	}
	if _, ok := checks["git"]; !ok {
		t.Error("windows was not asked for git")
	}
}
