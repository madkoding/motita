package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/sandbox"
)

// --- the first-run wizard ----------------------------------------------------

// TestConfigCommandRunsTheWizard: `config` must write a configuration at the given path
// and say how to use it, without needing a key or a config to exist beforehand.
func TestConfigCommandRunsTheWizard(t *testing.T) {
	// The wizard speaks the locale's language when nothing is configured; this test reads its
	// English, so the locale is pinned rather than taken from the machine running it.
	clearLocale(t, "en_US.UTF-8")
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "motita.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"config", "-config", path},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n\n\n1\n3\n\n"),
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the configuration must exist: %v", err)
		}
		text := out.String()
		if !strings.Contains(text, "Welcome! Let's connect motita to an AI model") {
			t.Errorf("the wizard must introduce itself: %q", text)
		}
		// It must prove the generated file loads, which is the point of the wizard.
		if !strings.Contains(text, "the configuration loads") {
			t.Errorf("the wizard must check what it wrote: %q", text)
		}
	})
}

// TestInitWithoutConfigUsesADefaultPath: with no -config the destination is
// ./motita.yaml, so the command is usable on its own.
// The wizard's default destination is the motita home, not the working directory: the file it
// writes is the one the program looks for on the next run, and a user with no ~/.motita gets
// one created as part of writing it.
func TestInitWithoutConfigWritesInTheMotitaHome(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"config"},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n\n\n1\n3\n\n"),
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		want := filepath.Join(home, ".motita", "motita.yaml")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("the wizard should write %s: %v", want, err)
		}
		if _, err := os.Stat("motita.yaml"); err == nil {
			t.Error("nothing should be left in the working directory")
		}
	})
}

// With no HOME there is nowhere to put a home, so the old behaviour stands: the working
// directory. A stripped environment (cron, a minimal container) must still be able to configure
// itself rather than refusing for want of a variable.
func TestInitWithoutHomeFallsBackToTheWorkingDirectory(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", "")
		silence(t)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"config"},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n\n\n1\n3\n\n"),
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if _, err := os.Stat("motita.yaml"); err != nil {
			t.Errorf("with no HOME the working directory is used: %v", err)
		}
	})
}

// TestInitCancelledIsNotAFailure: backing out with q is a normal outcome and must
// exit 0 with a clear message.
func TestInitCancelledIsNotAFailure(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "motita.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"config", "-config", path},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("q\n"),
		})
		if code != Success {
			t.Errorf("cancelling must not be an error: code = %d", code)
		}
		if !strings.Contains(out.String(), "nothing was written") {
			t.Errorf("out = %q", out.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("nothing may be written when cancelled")
		}
	})
}

// TestInitReportsAWizardFailure: a wizard that cannot write must exit with the
// configuration error code, not silently succeed.
func TestInitReportsAWizardFailure(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{"config"},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("not-a-provider\nbad\nworse\n"),
		})
		if code != ConfigError {
			t.Errorf("code = %d, want %d", code, ConfigError)
		}
		if !strings.Contains(errs.String(), "❌") {
			t.Errorf("the failure must be reported: %q", errs.String())
		}
	})
}

// TestInitRefusesWhenTheGeneratedFileDoesNotLoad: if the wizard ever wrote
// something the loader rejects, the user has to hear it immediately instead of on
// the first task.
func TestInitRefusesWhenTheGeneratedFileDoesNotLoad(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"config", "-config", path},
			Out:  &out,
			Err:  &errs,
			// A wizard that writes an invalid file on purpose.
			RunOnboard: func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error) {
				if err := os.WriteFile(path, []byte("llm:\n  provider: telepathy\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return onboard.Result{ConfigPath: path}, nil
			},
		})
		if code != ConfigError {
			t.Errorf("code = %d, want %d", code, ConfigError)
		}
		if !strings.Contains(errs.String(), "does not load") {
			t.Errorf("errs = %q", errs.String())
		}
	})
}

// TestInitUsesTheInjectedWizard: the flag must hand the real streams to the wizard,
// so the conversation is the documented one.
func TestInitUsesTheInjectedWizard(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		called := false
		var out bytes.Buffer
		code := Run(Options{
			Args: []string{"config", "-config", path},
			Out:  &out,
			Err:  &out,
			RunOnboard: func(
				_ context.Context, in io.Reader, w io.Writer, gotPath string, preset onboard.Answers,
			) (onboard.Result, error) {
				called = true
				if gotPath != path {
					t.Errorf("the wizard got %q, want %q", gotPath, path)
				}
				if !presetAsksEverything(preset) {
					t.Errorf("the wizard must be asked everything: %+v", preset)
				}
				fmt.Fprint(w, "dialogue")
				// A real wizard leaves a loadable file behind: app checks it.
				if err := os.WriteFile(gotPath, []byte("anchor:\n  kind: command\n  command: true\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return onboard.Result{
					ConfigPath: gotPath,
					Provider:   onboard.Providers()[0],
					Model:      onboard.Providers()[0].Models[0].ID,
				}, nil
			},
		})
		if code != Success {
			t.Fatalf("code = %d", code)
		}
		if !called {
			t.Fatal("the wizard must be called")
		}
		if !strings.Contains(out.String(), "dialogue") {
			t.Errorf("the wizard writes to the same output: %q", out.String())
		}
	})
}

// presetAsksEverything reports whether the wizard was given no answer in advance.
func presetAsksEverything(preset onboard.Answers) bool {
	noLLMPreset := preset.Provider == "" && preset.Model == "" && preset.APIKey == ""
	return noLLMPreset && preset.AnchorCommand == "" && len(preset.AnchorArgs) == 0
}

// --- Auto-trigger onboarding ------------------------------------------------

// TestAutoOnboardingWhenTUILaunchesWithoutConfig: a bare `motita` with no
// configuration file anywhere must trigger the wizard before the TUI starts,
// rather than showing a blank chat with no provider. The wizard writes the file,
// and the run then proceeds to load it.
func TestAutoOnboardingWhenTUILaunchesWithoutConfig(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)

		wizardRan := false
		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{}, // bare invocation → TUI
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader("openai\n\n\n1\n3\n\n"),
			RunOnboard: func(
				_ context.Context, in io.Reader, w io.Writer, gotPath string, preset onboard.Answers,
			) (onboard.Result, error) {
				wizardRan = true
				// Write a valid config so the rest of the run can proceed.
				if err := os.MkdirAll(filepath.Dir(gotPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(gotPath, []byte("anchor:\n  kind: command\n  command: true\nllm:\n  provider: openai\n  "+
					"api_key: test\n  model: gpt-4o-mini\nagent:\n  log_level: error\n  log_console: false\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return onboard.Result{ConfigPath: gotPath, Provider: onboard.Providers()[0], Model: "gpt-4o-mini"}, nil
			},
			RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
				return Success
			},
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if !wizardRan {
			t.Error("the wizard must auto-trigger when no config exists")
		}
	})
}

// TestAutoOnboardingDoesNotTriggerWhenConfigExists: a bare `motita` with a
// configuration file in the home must NOT trigger the wizard — the user already
// has a setup, and running the wizard would overwrite it.
func TestAutoOnboardingDoesNotTriggerWhenConfigExists(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)

		// Write a valid config in the home.
		homeFile := filepath.Join(home, ".motita", "motita.yaml")
		if err := os.MkdirAll(filepath.Dir(homeFile), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(
			t,
			homeFile,
			"anchor:\n  kind: command\n  command: true\nllm:\n  provider: openai\n  api_key: test\n  model: "+
				"gpt-4o-mini\nagent:\n  log_level: error\n  log_console: false\n",
		)

		wizardRan := false
		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{},
			Out:  &out,
			Err:  &errs,
			RunOnboard: func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error) {
				wizardRan = true
				return onboard.Result{}, nil
			},
			RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
				return Success
			},
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if wizardRan {
			t.Error("the wizard must NOT trigger when a config file already exists")
		}
	})
}

// TestAutoOnboardingDoesNotTriggerForDiagnosticModes: -validate-config and the
// other non-interactive modes must not trigger the wizard.
func TestAutoOnboardingDoesNotTriggerForDiagnosticModes(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", t.TempDir())
		silence(t)

		for _, args := range [][]string{
			{"-validate-config"},
			{"-isolation"},
			{"-version"},
		} {
			wizardRan := false
			var out, errs bytes.Buffer
			code := Run(Options{
				Args: args,
				Out:  &out,
				Err:  &errs,
				RunOnboard: func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error) {
					wizardRan = true
					return onboard.Result{}, nil
				},
			})
			// Some of these return Success, some ConfigError (no key), but the
			// point is the wizard must never have run.
			_ = code
			if wizardRan {
				t.Errorf("the wizard must not trigger for %v", args)
			}
		}
	})
}

// TestAutoOnboardingWizardFailure: when the auto-triggered wizard returns an
// error (not a cancellation), the run must abort with ConfigError — the
// program must never continue to the TUI with no configuration written.
func TestAutoOnboardingWizardFailure(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", t.TempDir())
		silence(t)

		var out, errs bytes.Buffer
		code := Run(Options{
			Args:  []string{},
			Out:   &out,
			Err:   &errs,
			Stdin: strings.NewReader(""),
			RunOnboard: func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error) {
				return onboard.Result{}, errors.New("simulated wizard failure")
			},
		})
		if code != ConfigError {
			t.Fatalf("code = %d, want ConfigError; errs = %q", code, errs.String())
		}
	})
}
