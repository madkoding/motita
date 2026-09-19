package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The implicit ./starlight.yaml branch: with no -config, a file in the working
// directory is used. It is the path a user gets by running the command in a folder
// where they had already run the wizard, and it has two outcomes that were never
// exercised: a file that cannot be loaded without a key, and one that cannot be
// loaded at all.

// TestImplicitConfigIsUsedWhenPresent: the file next to the working directory is
// picked up without being named on the command line.
func TestImplicitConfigIsUsedWhenPresent(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir, err := os.Getwd()
		if err != nil {
			t.Fatalf("could not read the working directory: %v", err)
		}
		mustWrite(t, filepath.Join(dir, "starlight.yaml"), `sandbox:
  kind: none
llm:
  provider: openai
  api_key: x
  model: mock
agent:
  log_level: error
  log_console: false
`)
		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config"},
			Out:  &out,
			Err:  &errs,
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		if !strings.Contains(out.String(), "valid configuration") {
			t.Errorf("the implicit file must be used: %q", out.String())
		}
	})
}

// TestImplicitConfigWithoutAKeyIsAcceptedForDiagnostics: -validate-config and
// -isolation do not talk to the provider, so a file with no key still validates.
// Without this branch the command would demand a key just to check a file.
func TestImplicitConfigWithoutAKeyIsAcceptedForDiagnostics(t *testing.T) {
	for _, flag := range []string{"-validate-config", "-isolation"} {
		t.Run(flag, func(t *testing.T) {
			inTempDir(t, func() {
				silence(t)
				dir, err := os.Getwd()
				if err != nil {
					t.Fatalf("could not read the working directory: %v", err)
				}
				// No api_key anywhere: not in the file, not in the environment.
				mustWrite(t, filepath.Join(dir, "starlight.yaml"), `sandbox:
  kind: none
llm:
  provider: openai
  model: mock
agent:
  log_level: error
  log_console: false
`)
				var out, errs bytes.Buffer
				code := Run(Options{
					Args: []string{flag},
					Out:  &out,
					Err:  &errs,
				})
				if code != Success {
					t.Fatalf("%s: code = %d, errs = %q", flag, code, errs.String())
				}
				if strings.Contains(errs.String(), "key is missing") {
					t.Errorf("%s must not complain about a missing key: %q", flag, errs.String())
				}
			})
		})
	}
}

// TestImplicitConfigThatCannotBeLoadedIsReported: a file that is broken beyond the
// missing key (a malformed document) is a configuration error, reported with the
// exit code that distinguishes it from a task failure.
func TestImplicitConfigThatCannotBeLoadedIsReported(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		dir, err := os.Getwd()
		if err != nil {
			t.Fatalf("could not read the working directory: %v", err)
		}
		// A tab in the indentation is not valid YAML.
		mustWrite(t, filepath.Join(dir, "starlight.yaml"), "llm:\n\tprovider: openai\n")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config"},
			Out:  &out,
			Err:  &errs,
		})
		if code != ConfigError {
			t.Fatalf("code = %d, want ConfigError; errs = %q", code, errs.String())
		}
		// The message names the file and the reason, which is what makes a broken
		// configuration fixable without guessing.
		if !strings.Contains(errs.String(), "starlight.yaml") || !strings.Contains(errs.String(), "invalid YAML") {
			t.Errorf("the failure must name the file and the reason: %q", errs.String())
		}
	})
}

// TestNoConfigFileFallsBackToTheEnvironment: with no file at all the defaults plus
// the environment are used, which is what makes the interface start from an
// exported key alone.
func TestNoConfigFileFallsBackToTheEnvironment(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		t.Setenv("STARLIGHT_LLM_API_KEY", "a-key-from-env")
		t.Setenv("STARLIGHT_LLM_PROVIDER", "openai")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config"},
			Out:  &out,
			Err:  &errs,
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
	})

	// And an environment that cannot be parsed is reported rather than ignored.
	inTempDir(t, func() {
		silence(t)
		t.Setenv("STARLIGHT_LLM_TIMEOUT", "not-a-duration")
		var out, errs bytes.Buffer
		code := Run(Options{Args: []string{"-validate-config"}, Out: &out, Err: &errs})
		if code != ConfigError {
			t.Fatalf("code = %d, want ConfigError; errs = %q", code, errs.String())
		}
	})
}
