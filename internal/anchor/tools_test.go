package anchor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestADirectCheckFindsTheAgentsTools replays the real session: the gate's program had been
// installed by the agent in its tools directory, and the anchor failed in 3 ms because it was not
// on PATH. With WithTools, a check run without the sandbox finds it by name.
func TestADirectCheckFindsTheAgentsTools(t *testing.T) {
	tools := t.TempDir()
	bin := filepath.Join(tools, "tools", "fake", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "fakegate"), []byte("#!/bin/sh\necho gate-ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Anchor{Kind: "command", Command: "fakegate", ExpectOutput: "gate-ok"}

	if res := New(cfg, t.TempDir(), nil).Validate(context.Background()); res.Pass {
		t.Fatal("without the tools directory the program must not be found")
	}
	res := New(cfg, t.TempDir(), nil).WithTools(tools, time.Minute).Validate(context.Background())
	if !res.Pass {
		t.Fatalf("with the tools directory the check must pass: %+v", res)
	}
}

// TestADetectedCheckGetsTheCheckTimeout: the detected gate gets at least the check timeout; an
// explicit check keeps the one its author chose.
func TestADetectedCheckGetsTheCheckTimeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".motita/anchor", "echo x\n")
	cfg := autoCfg()
	cfg.Timeout = 120 * time.Second

	checks := New(cfg, dir, nil).WithTools("", 15*time.Minute).detectChecks()
	if len(checks) != 1 || checks[0].Timeout != 15*time.Minute {
		t.Fatalf("a detected check must get the check timeout, got %+v", checks)
	}
	checks = New(cfg, dir, nil).WithTools("", time.Second).detectChecks()
	if checks[0].Timeout != 120*time.Second {
		t.Errorf("a shorter check timeout must not shorten the configured one, got %s", checks[0].Timeout)
	}
}
