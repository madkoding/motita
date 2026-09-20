package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain puts every test in this package under a HOME of its own.
//
// The defaults are computed from HOME, so without this a test's result would depend on where the
// person running the suite happens to live — and, worse, a test of "nothing is written to the
// working directory" could pass or fail according to what is already in their ~/.starlight.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "starlight-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

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

// A file that names no paths gets the home's, wherever the file itself is. The rule is that
// starlight's state lives under ~/.starlight; a configuration kept somewhere else does not move
// the state with it unless it says so.
func TestAPathlessFileUsesTheHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, "llm:\n  provider: openai\n  api_key: x\n  model: m\n")

	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	home := Dir()
	checks := []struct{ name, got, want string }{
		{"workspace", cfg.Agent.WorkspaceDir, filepath.Join(home, "workspace")},
		{"log", cfg.Agent.LogFile, filepath.Join(home, "workspace", "starlight.log")},
		{"skills", cfg.Skills.Dir, filepath.Join(home, "skills")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if strings.HasPrefix(cfg.Agent.WorkspaceDir, dir) {
		t.Error("nothing may land beside the file when the file did not ask for it")
	}
}

// A run with NO file at all still belongs under the home: without that, a first run before the
// wizard has written anything put its workspace in the working directory, which is exactly the
// scattering the home exists to prevent.
func TestNoFileUsesTheHome(t *testing.T) {
	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	home := Dir()
	checks := []struct{ name, got, want string }{
		{"workspace", cfg.Agent.WorkspaceDir, filepath.Join(home, "workspace")},
		{"log", cfg.Agent.LogFile, filepath.Join(home, "workspace", "starlight.log")},
		{"skills", cfg.Skills.Dir, filepath.Join(home, "skills")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// The working directory is NOT moved when it is named with ".": that is an instruction — work
// where I am standing — and it is how a user points starlight at the project in front of them.
func TestWorkingDirectoryIsNotMoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	mustWriteConfig(t, path, `llm:
  provider: openai
  api_key: x
  model: m
agent:
  workspace_dir: "."
`)
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatalf("LoadWithoutKey: %v", err)
	}
	if cfg.Agent.WorkspaceDir != "." {
		t.Errorf("workspace_dir = %q, want it left as the working directory", cfg.Agent.WorkspaceDir)
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
//
// The defaults are the HOME's paths in that case, because "there is a home" and "the file is in
// the working directory" are independent facts.
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
	// The file in the working directory supplies base ".", so a path it names explicitly is
	// untouched; the workspace it does not name comes from the home-aware default.
	if cfg.Agent.WorkspaceDir != filepath.Join(Dir(), "workspace") {
		t.Errorf("workspace_dir = %q, want the home default", cfg.Agent.WorkspaceDir)
	}
	if cfg.Skills.Dir != filepath.Join(Dir(), "skills") {
		t.Errorf("skills.dir = %q, want the home default", cfg.Skills.Dir)
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

// With no HOME the defaults are the working-directory paths they have always been: a stripped
// environment has nowhere else to put the state, and refusing to run would be worse than using
// the directory it is already in.
func TestNoHomeKeepsTheWorkingDirectoryDefaults(t *testing.T) {
	t.Setenv("HOME", "")
	if got := defaultWorkspaceDir(); got != "./workspace" {
		t.Errorf("workspace = %q, want the working-directory form", got)
	}
	if got := defaultLogFile(); got != "./workspace/starlight.log" {
		t.Errorf("log = %q, want the working-directory form", got)
	}
	if got := defaultSkillsDir(); got != "skills" {
		t.Errorf("skills = %q, want the working-directory form", got)
	}
	if got := File(); got != "" {
		t.Errorf("File() = %q, want empty", got)
	}
}

// A relative path with neither a file nor a home keeps the relative form: there is no anchor to
// resolve it against, and inventing one would send the state somewhere nobody chose.
func TestResolvePathsWithNothingToAnchorOn(t *testing.T) {
	t.Setenv("HOME", "")
	c := Default()
	before := c.Agent.WorkspaceDir
	resolvePaths(&c, "")
	if c.Agent.WorkspaceDir != before {
		t.Errorf("workspace = %q, want it unchanged (%q)", c.Agent.WorkspaceDir, before)
	}
}

// --- validating the effect --------------------------------------------------

// (Color and RGB were removed when the typewriter effect stopped carrying a phosphor: the
// sweep colours are constants in the renderer and have nothing to validate here.)

// A negative speed is rejected rather than clamped: it would make the reveal run backwards, and
// a number the user typed wrong must be reported, not silently corrected.
func TestANegativeTypewriterSpeedIsRejected(t *testing.T) {
	cfg := Default()
	cfg.CRT.TypewriterCPS = -1
	err := cfg.validate(false)
	if err == nil {
		t.Fatal("a negative speed must be rejected")
	}
	if !strings.Contains(err.Error(), "typewriter_cps") {
		t.Errorf("the failure must name the field: %v", err)
	}
}
