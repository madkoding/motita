package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The starlight home: one folder the user can find, back up or delete as a unit, instead of the
// configuration landing beside whatever project happened to be open.

func TestDirIsUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".starlight")
	if got := Dir(); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
	if got := File(); got != filepath.Join(want, "starlight.yaml") {
		t.Fatalf("File() = %q", got)
	}
}

// With no HOME there is no home to use, and the empty result is what tells the callers to keep
// their relative paths. Guessing ("./.starlight" or "/tmp") would put state somewhere the user
// did not choose; a stripped environment is a real case — cron, a minimal container.
func TestDirIsEmptyWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := Dir(); got != "" {
		t.Fatalf("Dir() = %q, want empty when there is no HOME", got)
	}
	if got := File(); got != "" {
		t.Fatalf("File() = %q, want empty when there is no HOME", got)
	}
}

// Whitespace in HOME is not a home either: it would produce a path like "  /.starlight".
func TestDirIgnoresBlankHome(t *testing.T) {
	t.Setenv("HOME", "   ")
	if got := Dir(); got != "" {
		t.Fatalf("Dir() = %q, want empty for a blank HOME", got)
	}
}

// --- the relative defaults follow the file ---------------------------------

// The rule that makes the home work: a relative path the DEFAULT supplied resolves beside the
// configuration file. With the file in ~/.starlight, the workspace, the log and the skills live
// there instead of being scattered through the project the user was working in.
func TestRelativeDefaultsFollowTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, "llm:\n  provider: openai\n  api_key: x\n  model: m\n")

	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	checks := []struct{ name, got, want string }{
		{"workspace", cfg.Agent.WorkspaceDir, filepath.Join(dir, "workspace")},
		{"log", cfg.Agent.LogFile, filepath.Join(dir, "workspace", "starlight.log")},
		{"skills", cfg.Skills.Dir, filepath.Join(dir, "skills")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// A relative path WRITTEN IN THE FILE is relative to the file, which is the convention every
// configuration format follows: it is what makes a configuration movable, and what keeps the
// program's state with its own file instead of with whatever directory it was started from.
func TestPathsWrittenInTheFileResolveBesideIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, `llm:
  provider: openai
  api_key: x
  model: m
agent:
  workspace_dir: ./mi-workspace
  log_file: ./mi.log
skills:
  dir: ./mis-skills
`)

	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	checks := []struct{ name, got, want string }{
		{"workspace", cfg.Agent.WorkspaceDir, filepath.Join(dir, "mi-workspace")},
		{"log", cfg.Agent.LogFile, filepath.Join(dir, "mi.log")},
		{"skills", cfg.Skills.Dir, filepath.Join(dir, "mis-skills")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// An ABSOLUTE path is left alone: a user who wrote /srv/workspace meant that directory, and
// resolving it against anything would be inventing a location they did not ask for.
func TestAbsolutePathsAreLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, `llm:
  provider: openai
  api_key: x
  model: m
agent:
  workspace_dir: /srv/workspace
  log_file: /var/log/starlight.log
skills:
  dir: /usr/share/starlight/skills
`)

	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	for _, c := range []struct{ name, got, want string }{
		{"workspace", cfg.Agent.WorkspaceDir, "/srv/workspace"},
		{"log", cfg.Agent.LogFile, "/var/log/starlight.log"},
		{"skills", cfg.Skills.Dir, "/usr/share/starlight/skills"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want it untouched", c.name, c.got)
		}
	}
}

// An empty path stays empty for resolvePaths to leave alone: validate fills it with the default
// afterwards, and joining "" would produce a trailing slash that then reads as a directory.
func TestEmptyPathsAreLeftForValidate(t *testing.T) {
	c := Default()
	c.Agent.WorkspaceDir = ""
	c.Skills.Dir = ""
	resolvePaths(&c, "/base")
	if c.Agent.WorkspaceDir != "" || c.Skills.Dir != "" {
		t.Fatalf("empty paths must be left for validate, got %q and %q", c.Agent.WorkspaceDir, c.Skills.Dir)
	}
}

// A file in the working directory keeps the old behaviour exactly: base is ".", and every path
// is what it always was. This is what makes the change safe for the setups that already exist.
func TestFileInTheWorkingDirectoryKeepsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, "llm:\n  provider: openai\n  api_key: x\n  model: m\n")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(previous)

	cfg, err := LoadWithoutKey("starlight.yaml")
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	if cfg.Agent.WorkspaceDir != "./workspace" {
		t.Errorf("workspace_dir = %q, want the relative default", cfg.Agent.WorkspaceDir)
	}
	if cfg.Skills.Dir != "skills" {
		t.Errorf("skills.dir = %q, want the relative default", cfg.Skills.Dir)
	}
}

// resolvePaths is a no-op for the two bases that mean "here", so it cannot alter a path it has no
// business touching.
func TestResolvePathsIgnoresHere(t *testing.T) {
	for _, base := range []string{"", "."} {
		c := Default()
		before := c.Agent.WorkspaceDir
		resolvePaths(&c, base)
		if c.Agent.WorkspaceDir != before {
			t.Errorf("base %q changed the workspace to %q", base, c.Agent.WorkspaceDir)
		}
	}
}

func mustWriteConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
