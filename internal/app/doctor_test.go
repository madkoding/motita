package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/doctor"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// fakeMachine is a machine where every environment check passes, so a test sees only the
// configuration's lines change the outcome.
func fakeMachine(dns error) *doctor.Env {
	return &doctor.Env{
		Version: "v9", Goos: "linux", Goarch: "amd64", Exe: "/usr/bin/motita", Shell: "/bin/sh",
		Getenv:   func(k string) string { return map[string]string{"PATH": "/usr/bin"}[k] },
		Stat:     os.Stat,
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Resolve:  func(context.Context, string) ([]string, error) { return nil, dns },
		DNS:      func() string { return "/etc/resolv.conf" },
		Timeout:  time.Second,
	}
}

func TestDoctorIsACommandWord(t *testing.T) {
	fl, err := parse([]string{"-config", "x.yaml", "doctor"})
	if err != nil || !fl.doctor || fl.configPath != "x.yaml" {
		t.Fatalf("parse = %+v, %v", fl, err)
	}
	if !strings.Contains(Usage(), "doctor") {
		t.Error("the usage does not list doctor")
	}
}

func TestDoctorWithoutAConfigurationWarns(t *testing.T) {
	silence(t)
	t.Setenv("HOME", t.TempDir())
	inTempDir(t, func() {
		var out bytes.Buffer
		code := Run(Options{Args: []string{"doctor"}, Out: &out, DoctorEnv: fakeMachine(nil)})
		if code != Success {
			t.Fatalf("code = %d:\n%s", code, out.String())
		}
		wants := []string{"motita v9 (linux/amd64)", "no configuration file yet: run motita config", "worth a look"}
		for _, want := range wants {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q:\n%s", want, out.String())
			}
		}
	})
}

func TestDoctorChecksTheConfigurationAndTheSandbox(t *testing.T) {
	silence(t)
	path := filepath.Join(t.TempDir(), "motita.yaml")
	// No key: the doctor, like -validate-config, does not call the provider.
	mustWrite(t, path, "anchor:\n  kind: command\n  command: \"true\"\n")
	var out bytes.Buffer
	code := Run(Options{Args: []string{"doctor", "-config", path}, Out: &out, DoctorEnv: fakeMachine(nil)})
	if code != Success {
		t.Fatalf("code = %d:\n%s", code, out.String())
	}
	for _, want := range []string{"✅ config", path + ": task_source=", "✅ sandbox", "isolation ["} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorReportsWhatTheSandboxCouldNotApply(t *testing.T) {
	silence(t)
	path := filepath.Join(t.TempDir(), "motita.yaml")
	mustWrite(t, path, "sandbox:\n  kind: cgroups\n  cgroups: on\n  cgroup_root: /a/path/that/does/not/exist\n")
	var out bytes.Buffer
	Run(Options{Args: []string{"doctor", "-config", path}, Out: &out, DoctorEnv: fakeMachine(nil)})
	if !strings.Contains(out.String(), "sandbox   not applied: ") {
		t.Errorf("no not-applied line:\n%s", out.String())
	}
}

func TestDoctorFailsOnABrokenMachineOrConfiguration(t *testing.T) {
	silence(t)
	good := filepath.Join(t.TempDir(), "motita.yaml")
	mustWrite(t, good, "anchor:\n  kind: command\n  command: \"true\"\n")
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	mustWrite(t, bad, "sandbox: [this is not a mapping\n")

	cases := []struct {
		name string
		op   Options
		want string
	}{
		{"dns", Options{Args: []string{"doctor", "-config", good}, DoctorEnv: fakeMachine(errors.New("no servers"))},
			"does not resolve: no servers"},
		{"config", Options{Args: []string{"doctor", "-config", bad}, DoctorEnv: fakeMachine(nil)}, "❌ config"},
		{"logger", Options{Args: []string{"doctor", "-config", good}, DoctorEnv: fakeMachine(nil),
			NewLogger: func(config.Agent) (*logx.Logger, error) { return nil, fmt.Errorf("simulated logger failure") }},
			"simulated logger failure"},
		{"sandbox", Options{Args: []string{"doctor", "-config", good}, DoctorEnv: fakeMachine(nil),
			NewSandbox: func(sandbox.Options) (*sandbox.Sandbox, error) {
				return nil, fmt.Errorf("simulated sandbox failure")
			}},
			"could not prepare the sandbox: simulated sandbox failure"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		c.op.Out = &out
		if code := Run(c.op); code != RunError {
			t.Errorf("%s: code = %d, want RunError:\n%s", c.name, code, out.String())
		}
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: missing %q:\n%s", c.name, c.want, out.String())
		}
	}
}

func TestDoctorChecksThisProcessByDefault(t *testing.T) {
	silence(t)
	t.Setenv("HOME", t.TempDir())
	// The real machine's checks include a DNS lookup of api.github.com, read through
	// net.DefaultResolver at the call. A resolver that refuses every query keeps that check
	// offline: what is under test is that the process's own Env is used, not the network.
	saved := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("offline")
	}}
	t.Cleanup(func() { net.DefaultResolver = saved })
	inTempDir(t, func() {
		var out bytes.Buffer
		Run(Options{Args: []string{"doctor"}, Out: &out, Version: "v7", Goos: "linux", Goarch: "amd64"})
		if !strings.Contains(out.String(), "motita v7 (") {
			t.Errorf("the report is not this process's:\n%s", out.String())
		}
	})
}
