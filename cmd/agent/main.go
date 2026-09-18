// Command starlight is a 3-layer autonomous agent for i386 machines.
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
//	starlight -config configs/agent.yaml.example
//	starlight -config configs/cases/1-development.yaml -task "fix test X"
//	starlight -config configs/agent.yaml.example -validate-config
//
// This file is deliberately minimal: all the logic lives in internal/app, where it
// can actually be tested. Here only the real process (output, signals) and the
// exit code are wired up.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/madkoding/starlight/internal/app"
)

// version is injected with -ldflags "-X main.version=v1.0.0".
var version = "dev"

// setupSignals wires a cancellable context to the provided signal channel
// (or creates one when nil). The context is cancelled first so interactive
// modes return immediately; the signal is then re-injected so app.Run can still
// perform graceful shutdown of any background work. Passing an explicit
// channel makes the function testable without needing the real signal package.
func setupSignals(injected chan os.Signal) (context.Context, chan os.Signal, func()) {
	signals := injected
	if signals == nil {
		signals = make(chan os.Signal, 2)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := func() {
		if injected == nil {
			signal.Stop(signals)
		}
		cancel()
	}
	go func() {
		sig, ok := <-signals
		if !ok {
			return
		}
		cancel()
		signals <- sig
	}()
	return ctx, signals, stop
}

func main() {
	ctx, signals, stop := setupSignals(nil)
	defer stop()

	code := app.Run(app.Options{
		Args:    os.Args[1:],
		Out:     os.Stdout,
		Err:     os.Stderr,
		Version: version,
		Goos:    runtime.GOOS,
		Goarch:  runtime.GOARCH,
		Signals: signals,
		BaseCtx: ctx,
	})
	os.Exit(code)
}
