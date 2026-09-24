package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadWithoutKeyDoesNotReplaceTheFile: the failure this prevents, found when
// testing the real binary, was that `-validar-config` with an invalid YAML
// reported "valid configuration" and exited with 0, because the failed load was
// silently replaced by the default values. It hid exactly the error the
// validation mode exists to detect.
func TestLoadWithoutKeyDoesNotReplaceTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("task_source:\n  kind: telepathy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadWithoutKey(path); err == nil {
		t.Fatal("an unknown source kind must fail even when the key is not required")
	} else if !strings.Contains(err.Error(), "telepathy") {
		t.Errorf("the error must say which value is invalid: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load must fail as well")
	}
}

func TestLoadWithoutKeyToleratesMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-key.yaml")
	content := "anchor:\n  kind: command\n  command: make\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// With no key: Load fails, LoadWithoutKey does not.
	if _, err := Load(path); err == nil {
		t.Fatal("Load must require the key")
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey must not require the key: %v", err)
	}
	if cfg.Anchor.Command != "make" {
		t.Errorf("the file was not read: %+v", cfg.Anchor)
	}
}

func TestLoadWithoutKeyKeepsValidatingEverythingElse(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		contains string
	}{
		{"unknown provider", "llm:\n  provider: wizard\n", "unknown llm.provider"},
		{"unknown sandbox", "sandbox:\n  kind: magic\n", "unknown sandbox.kind"},
		{"chroot without root", "sandbox:\n  kind: chroot\n", "requires 'root'"},
		{"unknown key", "llm:\n  color: blue\n", "color"},
		{"invalid level", "agent:\n  log_level: verbose\n", "unknown agent.log_level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.yaml")
			os.WriteFile(path, []byte(tc.content), 0o644)
			_, err := LoadWithoutKey(path)
			if err == nil {
				t.Fatalf("an error was expected for %q", tc.content)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, it was expected to contain %q", err, tc.contains)
			}
		})
	}
}

func TestMissingFileFails(t *testing.T) {
	if _, err := LoadWithoutKey("/does/not/exist/config.yaml"); err == nil {
		t.Fatal("a missing file must fail, not fall back to the default values")
	}
}

func TestGatewayValidation(t *testing.T) {
	cases := []struct {
		name    string
		gateway func(*Config)
		wantErr string
	}{
		{"the defaults are valid", func(*Config) {}, ""},
		{"loopback is valid", func(c *Config) { c.Gateway.Listen = "127.0.0.1:8787" }, ""},
		{"localhost is loopback", func(c *Config) { c.Gateway.Listen = "localhost:8787" }, ""},
		{"::1 is loopback", func(c *Config) { c.Gateway.Listen = "[::1]:8787" }, ""},
		// Every address is acceptable, because the socket no longer decides who may connect: the
		// rules do. Each of these is a legitimate choice an operator makes with gateway.listen, and
		// refusing one would send them to a setting that is not what they were looking for.
		{"the wildcard is valid", func(c *Config) { c.Gateway.Listen = "0.0.0.0:8787" }, ""},
		{"a concrete LAN address is valid", func(c *Config) { c.Gateway.Listen = "192.168.100.90:8787" }, ""},
		{
			// An empty host means every interface on this machine, which is the DEFAULT anyway.
			"an empty host is valid",
			func(c *Config) { c.Gateway.Listen = ":8787" },
			"",
		},
		{
			"a bad address is refused",
			func(c *Config) { c.Gateway.Listen = "not an address" },
			"gateway.listen",
		},
		{
			"a negative body cap is refused",
			func(c *Config) { c.Gateway.MaxBodyKB = -1 },
			"gateway.max_body_kb",
		},
		{
			// Zero is the built-in default and is accepted; a negative ceiling is a number
			// nobody meant, and taking it as the default would hide the typo that produced it.
			"a zero session ceiling means the default and is valid",
			func(c *Config) { c.Gateway.MaxSessions = 0 },
			"",
		},
		{
			"a positive session ceiling is valid",
			func(c *Config) { c.Gateway.MaxSessions = 32 },
			"",
		},
		{
			"a negative session ceiling is refused",
			func(c *Config) { c.Gateway.MaxSessions = -2 },
			"gateway.max_sessions",
		},
		{
			"an enabled gateway needs a token file",
			func(c *Config) { c.Gateway.TokenFile = "" },
			"gateway.token_file",
		},
		{
			"a blank token file is the same as none",
			func(c *Config) { c.Gateway.TokenFile = "   " },
			"gateway.token_file",
		},
		{
			// Port 0 is a legitimate ask (the kernel picks a free one), and an empty address means
			// "resolve the default" rather than being refused for a host it never had.
			"an empty listen address resolves to the default",
			func(c *Config) { c.Gateway.Listen = "" },
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.gateway(&cfg)
			err := cfg.validate(false)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("an error naming %q was expected", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error = %v, it must name %q", err, tc.wantErr)
			}
		})
	}
}

// A DISABLED gateway is not validated as if it were running: a configuration that turns the
// gateway off must not be refused for an address it will never bind.
func TestADisabledGatewaySkipsItsChecks(t *testing.T) {
	cfg := Default()
	cfg.Gateway.Enabled = false
	cfg.Gateway.Listen = "not an address"
	cfg.Gateway.TokenFile = ""
	cfg.Gateway.MaxBodyKB = -1
	if err := cfg.validate(false); err != nil {
		t.Errorf("a disabled gateway must not be validated as a running one: %v", err)
	}
}
