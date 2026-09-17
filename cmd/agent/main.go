// Command starlight-agent is a 3-layer autonomous agent for i386 machines.
//
// Architecture (see README.md):
//
//	Layer A — THE ANCHOR: deterministic validator, no reasoning. Compiled
//	         natively, it runs real checks and returns PASS/FAIL with JSON
//	         records.
//	Layer B — THE REASONING ENGINE: lightweight LLM client (OpenAI, Anthropic or
//	         Gemini), no SDK, with retries and exponential backoff.
//	Layer C — THE SANDBOX: isolated execution without Docker (temporary
//	         directory, process limits through ulimit, optional cgroups v1,
//	         optional chroot).
//
// Everything is configurable without recompiling: see configs/agent.yaml.example
// and configs/cases/*.yaml for three complete use cases.
//
// Usage:
//
//	starlight-agent -config configs/agent.yaml.example
//	starlight-agent -config configs/cases/1-development.yaml -task "fix test X"
//	starlight-agent -config configs/agent.yaml.example -validate-config
//
// This file is deliberately minimal: all the logic lives in internal/app, where it
// can actually be tested. Here only the real process (output, signals) and the
// exit code are wired up.
package main

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/madkoding/starlight/internal/app"
)

// version is injected with -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() {
	// The signal handler is installed here (not in app) so app does not depend
	// on the process' global resources.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	code := app.Run(app.Options{
		Args:    os.Args[1:],
		Out:     os.Stdout,
		Err:     os.Stderr,
		Version: version,
		Goos:    runtime.GOOS,
		Goarch:  runtime.GOARCH,
		Signals: signals,
	})
	os.Exit(code)
}
