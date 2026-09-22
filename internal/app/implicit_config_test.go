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
		// An empty home, so the file under test is the one in the working directory rather than
		// whatever the developer running the suite happens to have in ~/.starlight.
		t.Setenv("HOME", t.TempDir())
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
		// An empty home, so the file under test is the one in the working directory rather than
		// whatever the developer running the suite happens to have in ~/.starlight.
		t.Setenv("HOME", t.TempDir())
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

// The starlight home takes precedence over the working directory, which is the opposite of the
// usual project-local convention and deliberate: the file carries the credentials and the paths
// to the program's own state, so it belongs to the user rather than to whichever repository they
// happened to be standing in.
func TestTheHomeFileWinsOverTheWorkingDirectory(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)

		// A file in the working directory that would FAIL if it were used: a tab is not valid
		// YAML indentation. If the home wins, this one is never read and the run succeeds.
		dir, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		mustWrite(t, filepath.Join(dir, "starlight.yaml"), "llm:\n\tprovider: openai\n")

		// A valid file in the home.
		homeFile := filepath.Join(home, ".starlight", "starlight.yaml")
		if err := os.MkdirAll(filepath.Dir(homeFile), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		mustWrite(t, homeFile, `sandbox:
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
			t.Fatalf("the home file should have been used; code = %d, errs = %q", code, errs.String())
		}
		if strings.Contains(errs.String(), "invalid YAML") {
			t.Fatalf("the working-directory file must not have been read: %q", errs.String())
		}
	})
}

// An explicit -config still wins over both: naming a file is the most specific instruction a
// user can give, and quietly preferring a different one would be ignoring what they typed.
func TestExplicitConfigWinsOverTheHome(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)

		// A broken file in the home that must NOT be used.
		homeFile := filepath.Join(home, ".starlight", "starlight.yaml")
		if err := os.MkdirAll(filepath.Dir(homeFile), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		mustWrite(t, homeFile, "llm:\n\tprovider: openai\n")

		dir, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		named := filepath.Join(dir, "elegido.yaml")
		mustWrite(t, named, `sandbox:
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
			Args: []string{"-validate-config", "-config", named},
			Out:  &out,
			Err:  &errs,
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
	})
}

// With no configuration file at all the program starts from defaults, and an environment
// variable that cannot be parsed is still reported: a default run must not silently ignore a bad
// variable and then behave in a way the user never asked for.
func TestNoConfigWithABadEnvironmentIsReported(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", "")
		silence(t)
		t.Setenv("STARLIGHT_AGENT_MAX_RETRIES", "not-a-number")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config"},
			Out:  &out,
			Err:  &errs,
		})
		if code != ConfigError {
			t.Fatalf("code = %d, want ConfigError; errs = %q", code, errs.String())
		}
		if !strings.Contains(errs.String(), "STARLIGHT_AGENT_MAX_RETRIES") {
			t.Errorf("the failure must name the variable at fault: %q", errs.String())
		}
	})
}

// A broken file IN THE HOME is reported, with the code that distinguishes a configuration error
// from a task failure. The home is not a privileged location: a file the user wrote there is
// checked exactly like one named on the command line.
func TestBrokenHomeConfigIsReported(t *testing.T) {
	inTempDir(t, func() {
		home := t.TempDir()
		t.Setenv("HOME", home)
		silence(t)

		homeFile := filepath.Join(home, ".starlight", "starlight.yaml")
		if err := os.MkdirAll(filepath.Dir(homeFile), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// A tab in the indentation is not valid YAML.
		mustWrite(t, homeFile, "llm:\n\tprovider: openai\n")

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config"},
			Out:  &out,
			Err:  &errs,
		})
		if code != ConfigError {
			t.Fatalf("code = %d, want ConfigError; errs = %q", code, errs.String())
		}
		if !strings.Contains(errs.String(), "invalid YAML") {
			t.Errorf("the failure must say what is wrong: %q", errs.String())
		}
	})
}

// The diagnostic modes must not write anything.
//
// This was a real CI failure, not a hypothetical one. -validate-config opened the log, which
// CREATED the workspace directory to hold it; inside the CI container the configuration
// directory is mounted read-only, so validating a configuration that was perfectly valid failed
// on the file it tried to write. The same run also left a configs/workspace/ behind in the
// repository.
func TestValidateConfigWritesNothing(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", "")
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "elegido.yaml")
		mustWrite(t, cfgPath, `sandbox:
  kind: none
llm:
  provider: openai
  api_key: x
  model: mock
anchor:
  kind: command
  command: "true"
agent:
  log_level: error
  log_console: false
  workspace_dir: ./workspace
  log_file: ./workspace/starlight.log
`)
		// The directory holding the configuration is made read-only, which is how the CI mounts
		// it. A diagnostic that writes there fails.
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Skipf("could not make the directory read-only: %v", err)
		}
		defer os.Chmod(dir, 0o755)

		var out, errs bytes.Buffer
		code := Run(Options{
			Args: []string{"-validate-config", "-config", cfgPath},
			Out:  &out,
			Err:  &errs,
		})
		if code != Success {
			t.Fatalf("code = %d, errs = %q", code, errs.String())
		}
		// The workspace the file names must not have been created: creating it means writing.
		if _, err := os.Stat(filepath.Join(dir, "workspace")); err == nil {
			t.Error("-validate-config must not create the workspace")
		}
		// And the summary says the log goes to the console, which is what a diagnostic does.
		if !strings.Contains(out.String(), "log=(console only)") {
			t.Errorf("a diagnostic must not open a log file: %q", out.String())
		}
	})
}
