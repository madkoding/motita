// Package config defines the agent configuration and its loading from YAML and
// environment variables, with no external dependencies.
//
// The agent is 100% configurable without recompiling: no business decision
// (task source, validator, sandbox limits, LLM provider, prompt templates or
// final action) lives in the code.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Structs (one per YAML block)
// ---------------------------------------------------------------------------

// Config is the root of the configuration file.
type Config struct {
	TaskSource  TaskSource  `yaml:"task_source"`
	Anchor      Anchor      `yaml:"anchor"`
	Sandbox     Sandbox     `yaml:"sandbox"`
	LLM         LLM         `yaml:"llm"`
	Prompts     Prompts     `yaml:"prompts"`
	FinalAction FinalAction `yaml:"final_action"`
	Agent       Agent       `yaml:"agent"`
	Skills      Skills      `yaml:"skills"`
}

// TaskSource describes where the tasks come from.
type TaskSource struct {
	Kind     string            `yaml:"kind"`     // stdin | file | api | queue
	Path     string            `yaml:"path"`     // file
	Dir      string            `yaml:"dir"`      // queue
	URL      string            `yaml:"url"`      // api
	Method   string            `yaml:"method"`   // api: GET | POST
	Field    string            `yaml:"field"`    // field of the JSON/payload holding the task
	Interval time.Duration     `yaml:"interval"` // api/queue: polling
	Headers  map[string]string `yaml:"headers"`  // api
	Body     string            `yaml:"body"`     // api: body for POST
}

// Anchor is the deterministic validator (Layer A). It may be a single command
// or a list of checks; every one of them must pass.
type Anchor struct {
	Kind         string        `yaml:"kind"` // command | none
	Command      string        `yaml:"command"`
	Args         []string      `yaml:"args"`
	Timeout      time.Duration `yaml:"timeout"`
	ExpectExit   int           `yaml:"expect_exit"`
	ExpectOutput string        `yaml:"expect_output"` // optional regular expression
	Checks       []Check       `yaml:"checks"`
}

// Check is an extra check of the anchor.
type Check struct {
	Name         string        `yaml:"name"`
	Command      string        `yaml:"command"`
	Args         []string      `yaml:"args"`
	Timeout      time.Duration `yaml:"timeout"`
	ExpectExit   int           `yaml:"expect_exit"`
	ExpectOutput string        `yaml:"expect_output"`
}

// Sandbox describes the isolated execution environment (Layer C).
type Sandbox struct {
	Kind           string        `yaml:"kind"` // none | chroot | cgroups
	Root           string        `yaml:"root"` // chroot: root directory
	User           string        `yaml:"user"` // optional "uid:gid"
	MemoryMB       int           `yaml:"memory_mb"`
	CPUSeconds     int           `yaml:"cpu_seconds"`
	Processes      int           `yaml:"processes"`
	OpenFiles      int           `yaml:"open_files"`
	MaxFileSizeMB  int           `yaml:"max_file_size_mb"`
	IsolateNetwork bool          `yaml:"isolate_network"`
	Cgroups        string        `yaml:"cgroups"` // auto | on | off
	CgroupRoot     string        `yaml:"cgroup_root"`
	Timeout        time.Duration `yaml:"timeout"`
	KeepEphemeral  bool          `yaml:"keep_ephemeral"`
	MaxOutputKB    int           `yaml:"max_output_kb"`
}

// LLM describes the reasoning engine (Layer B).
type LLM struct {
	Provider       string        `yaml:"provider"` // openai | anthropic | gemini
	Model          string        `yaml:"model"`
	APIKey         string        `yaml:"api_key"`
	BaseURL        string        `yaml:"base_url"`
	MaxTokens      int           `yaml:"max_tokens"`
	Temperature    float64       `yaml:"temperature"`
	Timeout        time.Duration `yaml:"timeout"`
	MaxAttempts    int           `yaml:"max_attempts"`
	BackoffInitial time.Duration `yaml:"backoff_initial"`
	BackoffMax     time.Duration `yaml:"backoff_max"`
	Reasoning      Reasoning     `yaml:"reasoning"`
	Session        Session       `yaml:"session"`
	Skills         Skills        `yaml:"skills"`
}

// Skills points the agent at its procedure library: the directory of documents it may search
// and extend.
type Skills struct {
	// Dir is where the documents live. Empty means ./skills, relative to the working
	// directory, which is where a user will look for what the agent wrote.
	Dir string `yaml:"dir"`
	// MaxFileBytes caps one document, so a stray large file cannot be pulled into the
	// context as if it were a procedure.
	MaxFileBytes int `yaml:"max_file_bytes"`
}

// Session describes how a conversation is kept inside the model's context window.
type Session struct {
	// ContextWindow overrides the window derived from the model id. Zero means
	// "ask the model", which is what the built-in table is for; a value here wins,
	// because only the operator knows a local server configured lower.
	ContextWindow int `yaml:"context_window"`
	// Reserve is the room kept for the answer and the next tool round, so the
	// session compacts while the model still has space to reply.
	Reserve int `yaml:"reserve"`
	// CompactAt is the fraction of the usable window at which compaction triggers.
	CompactAt float64 `yaml:"compact_at"`
	// KeepRecent is how many recent messages are never summarised: the user is
	// still talking about them.
	KeepRecent int `yaml:"keep_recent"`
}

// Reasoning controls whether and how hard the model thinks before answering.
// Providers map the level to their own parameters:
//   - openai/o1/o3 and deepseek: reasoning_effort (low/medium/high)
//   - anthropic: thinking budget_tokens
//   - gemini: thinkingBudget
//   - ollama: passes reasoning_effort through the OpenAI-compatible endpoint
//
// If a provider does not support reasoning, the value is ignored.
type Reasoning struct {
	Enabled bool   `yaml:"enabled"`
	Level   string `yaml:"level"` // off, low, medium, high
}

// Template is a prompt with {{name}} variables.
type Template struct {
	System string `yaml:"system"`
	User   string `yaml:"user"`
}

// Prompts groups the templates used by the agent's main loop.
type Prompts struct {
	Analyze    Template `yaml:"analyze"`
	Plan       Template `yaml:"plan"`
	Execute    Template `yaml:"execute"`
	Synthesize Template `yaml:"synthesize"`
}

// FinalAction is what runs when the anchor gives PASS.
type FinalAction struct {
	Kind          string   `yaml:"kind"` // none | command | api | git_commit
	Command       string   `yaml:"command"`
	Args          []string `yaml:"args"`
	URL           string   `yaml:"url"`
	Method        string   `yaml:"method"`
	CommitMessage string   `yaml:"commit_message"`
}

// OnFailure is the escalation action run when the retries are exhausted.
type OnFailure struct {
	Kind    string `yaml:"kind"` // none | command
	Command string `yaml:"command"`
}

// Agent groups the parameters of the main loop.
type Agent struct {
	MaxRetries      int           `yaml:"max_retries"`
	SubtaskDepth    int           `yaml:"subtask_depth"`
	MaxTasks        int           `yaml:"max_tasks"`
	WorkspaceDir    string        `yaml:"workspace_dir"`
	LogFile         string        `yaml:"log_file"`
	LogLevel        string        `yaml:"log_level"`
	LogConsole      bool          `yaml:"log_console"`
	LogMaxMB        int           `yaml:"log_max_mb"`
	LogBackups      int           `yaml:"log_backups"`
	ShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout"`
	// ReadOnly is plan mode: the agent explores and plans, and every action that
	// could change the system is refused before it runs.
	ReadOnly bool `yaml:"read_only"`
	// Shell is the interpreter used for actions outside read-only mode. Empty means
	// the platform default (sh on Unix, the command processor on Windows).
	Shell string `yaml:"shell"`
	// Policy is what the agent may do without asking. See Policy below: it has two
	// layers, and only one of them is configurable.
	Policy    Policy    `yaml:"policy"`
	OnFailure OnFailure `yaml:"on_failure"`
}

// Policy is what the agent may do on its own, and it is deliberately two layers.
//
// The first layer is this struct: two settings the operator owns, because both are
// legitimate choices that depend on how starlight is being run.
//
// The second layer is NOT here, and its absence is the point. A short list of commands
// (see internal/policy) is refused whatever this block says: erasing a filesystem, the
// machine's power state, the partition table, a recursive forced delete of the tree the
// agent works under. There is no YAML key, no environment variable and no flag that
// relaxes them, because a guardrail an operator can switch off is a guardrail that will
// be switched off — during the incident it was meant for, by whoever wants the task to
// finish.
type Policy struct {
	// Enforce confirms before a consequential action runs. Turning it off restores the
	// older behaviour (the model's line is run as written), which is a defensible choice
	// for a batch job with nobody at the keyboard and is why the setting exists. It does
	// NOT reach the mandatory layer above.
	//
	// Default: true. An agent that has to be told to ask before acting is an agent that
	// acts without asking by default, which is the failure this exists to fix.
	Enforce bool `yaml:"enforce"`
	// Strict refuses what the policy cannot classify — an unknown program, a line that needs
	// a shell, a writer whose target is not in its arguments.
	//
	// Off (the default) means those are ASKED about instead. That is the honest default:
	// the set of programs a user's work needs is unbounded, refusing everything unlisted
	// makes the agent useless for the work it was pointed at, and a question costs one
	// keystroke. On is for a run where the operator would rather see a refusal than a
	// prompt.
	//
	// With Enforce off, the two interact in one direction only: a command that would be
	// ASKED about is then ALLOWED, and one that Strict REFUSES stays refused. Turning the
	// confirmation off cannot promote a refusal into a run.
	//
	// It does NOT reach the mandatory floor.
	Strict bool `yaml:"strict"`
}

// ---------------------------------------------------------------------------
// Default values
// ---------------------------------------------------------------------------

// Default returns a complete, usable configuration with no YAML file.
func Default() Config {
	return Config{
		TaskSource: TaskSource{
			Kind:     "stdin",
			Method:   "GET",
			Field:    "task",
			Interval: 30 * time.Second,
		},
		Anchor: Anchor{
			Kind:    "none",
			Timeout: 120 * time.Second,
		},
		Sandbox: Sandbox{
			Kind:          "none",
			Cgroups:       "auto",
			CgroupRoot:    "/sys/fs/cgroup",
			MemoryMB:      512,
			CPUSeconds:    60,
			Processes:     128,
			OpenFiles:     256,
			MaxFileSizeMB: 64,
			Timeout:       300 * time.Second,
			MaxOutputKB:   256,
		},
		LLM: LLM{
			Provider:       "openai",
			Model:          "gpt-4o-mini",
			BaseURL:        "https://api.openai.com/v1",
			MaxTokens:      2048,
			Temperature:    0.2,
			Timeout:        90 * time.Second,
			MaxAttempts:    3,
			BackoffInitial: time.Second,
			BackoffMax:     30 * time.Second,
			Reasoning: Reasoning{
				Enabled: false,
				Level:   "medium",
			},
		},
		Skills: Skills{
			Dir:          defaultSkillsDir(),
			MaxFileBytes: 64 * 1024,
		},
		Prompts: Prompts{
			Analyze:    BaseAnalyzeTemplate,
			Plan:       BasePlanTemplate,
			Execute:    BaseExecuteTemplate,
			Synthesize: BaseSynthesizeTemplate,
		},
		FinalAction: FinalAction{
			Kind:          "none",
			Method:        "POST",
			CommitMessage: "agent: {{task}}",
		},
		Agent: Agent{
			MaxRetries:   3,
			SubtaskDepth: 1,
			WorkspaceDir: defaultWorkspaceDir(),
			LogLevel:     "info",
			// The policy asks before a consequential action runs, and refuses what it
			// cannot classify. Both defaults are the cautious side of a choice the
			// operator still owns: see Policy.
			Policy: Policy{Enforce: true, Strict: false},
			// A file is always named. The conversational interface silences the console so
			// structured lines do not land in the middle of the chat, and with no file that
			// silence would be the whole log: a user reporting a problem from the chat would
			// have nothing to send. The path is relative to the working directory, beside the
			// workspace the agent already writes to.
			LogFile:         defaultLogFile(),
			LogConsole:      true,
			LogMaxMB:        5,
			LogBackups:      3,
			ShutdownTimeout: 15 * time.Second,
			OnFailure:       OnFailure{Kind: "none"},
		},
	}
}

// Dir is the default home for starlight's own state: the configuration file, the workspace the
// agent writes to, the log, and the library of skills.
//
// It is ~/.starlight. Everything the program owns lives under one folder the user can find,
// back up or delete as a unit, instead of the configuration landing in the current directory
// beside whatever project happened to be open.
//
// The HOME variable is read rather than the OS user database, because the program runs on
// minimal containers where the two disagree and HOME is the one that matches the shell the user
// is in. When it is unset — a stripped environment, a cron job — there is no sensible home, and
// the empty result tells the caller to keep the old relative paths rather than guess.
func Dir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".starlight")
	}
	return ""
}

// The defaults that keep starlight's own state under one roof. They are the home's paths when
// there is a home, and the old working-directory paths when there is not.
//
// They are computed rather than written out because HOME can change within a process's life in
// tests, and a package-level variable captured at init would freeze the first value and send
// later runs to the wrong place.
func defaultWorkspaceDir() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "workspace")
	}
	return "./workspace"
}

func defaultLogFile() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "workspace", "starlight.log")
	}
	return "./workspace/starlight.log"
}

func defaultSkillsDir() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "skills")
	}
	return "skills"
}

// File is the configuration file inside Dir, and the empty string when there is no home.
func File() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "starlight.yaml")
	}
	return ""
}

// resolvePaths makes every RELATIVE path in the configuration resolve beside the file itself,
// and falls back to the starlight home when the file provides no base at all.
//
// This is the convention a configuration file is expected to follow: a path written in a file is
// relative to that file, not to wherever the program happened to be started. git, ssh and systemd
// all read their own files this way, and it is what makes a configuration movable.
//
// It is also the rule that keeps everything inside ~/.starlight. With the file there,
// "./workspace" means ~/.starlight/workspace, and a file that names no workspace at all gets one
// under the home rather than in whatever directory the program was launched from. There is one
// exception, and it is the working directory itself: see below.
//
// Absolute paths are left alone: a user who wrote /srv/workspace meant that directory, and
// resolving it against anything would be inventing a location.
func resolvePaths(c *Config, base string) {
	if base == "" {
		base = Dir()
	}
	if base == "" {
		// No file and no home: there is nowhere to anchor a relative path, so they keep the
		// relative form they have always had. A stripped environment is a real case.
		return
	}
	join := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	// The working directory is NOT moved. "." is an instruction — work where I am standing — and
	// it is how a user points starlight at the project in front of them. A relative path that
	// names a SUBSET of it ("./workspace") is moved to the home, because that is program state
	// rather than the project; the project a user is working in is named with ".".
	if c.Agent.WorkspaceDir != "." {
		c.Agent.WorkspaceDir = join(c.Agent.WorkspaceDir)
	}
	c.Agent.LogFile = join(c.Agent.LogFile)
	c.Skills.Dir = join(c.Skills.Dir)
}

// LoadOrDefault applies environment variables to the default configuration when
// no file is present. It is used by the TUI so that env vars such as
// OLLAMA_API_KEY are picked up even without a starlight.yaml in the current
// directory.
func LoadOrDefault(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		// A run with no file still belongs under the home. Without this the workspace, the log
		// and the skills fell back to the working directory — which is precisely the scattering
		// the home exists to prevent, and it was the path taken by a first run before the wizard
		// has written anything.
		resolvePaths(&cfg, "")
		if err := ApplyEnvironment(&cfg); err != nil {
			return cfg, err
		}
		// An environment variable may name a workspace of its own, and it is applied after the
		// resolution above so that what it sets is what is used. It is the user's instruction,
		// and it is taken as written: a relative path still means, to the person who typed it,
		// relative to where they are.
		return cfg, nil
	}
	return LoadWithoutKey(path)
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------
func Load(path string) (Config, error) {
	return load(path, true)
}

// LoadWithoutKey does the same but without requiring llm.api_key.
//
// It exists for the modes that do not call the LLM (-validar-config,
// -aislamiento): there, not having a key is legitimate. What is NOT acceptable
// is for an invalid file to be silently replaced by the default configuration
// and then report "valid configuration": that would hide precisely the error
// that is being checked.
func LoadWithoutKey(path string) (Config, error) {
	return load(path, false)
}

func load(path string, requireKey bool) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("could not read the configuration %q: %w", path, err)
		}
		m, err := ParseYAML(data)
		if err != nil {
			return cfg, fmt.Errorf("invalid YAML in %q: %w", path, err)
		}
		if err := Decode(m, &cfg); err != nil {
			return cfg, fmt.Errorf("invalid configuration in %q: %w", path, err)
		}
		// Every relative path resolves beside the file, so a configuration is movable and the
		// program's state stays with it. Applied BEFORE validate, because validate is what fills
		// an empty path with the default, and a value arriving afterwards would keep the
		// default's relative form.
		resolvePaths(&cfg, filepath.Dir(path))
	}

	// Environment variables (they win over the YAML), including the standard
	// OPENAI_* names the README documents.
	if err := ApplyEnvironment(&cfg); err != nil {
		return cfg, err
	}

	if err := cfg.validate(requireKey); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks coherence and fills in what can be deduced, requiring the
// LLM key.
func (c *Config) Validate() error { return c.validate(true) }

// ValidateWithoutKey is the same but tolerates the absence of llm.api_key.
func (c *Config) ValidateWithoutKey() error { return c.validate(false) }

// validate is the body shared by Validate and ValidateWithoutKey.
func (c *Config) validate(requireKey bool) error {
	c.TaskSource.Kind = normalize(c.TaskSource.Kind)
	c.Anchor.Kind = normalize(c.Anchor.Kind)
	c.Sandbox.Kind = normalize(c.Sandbox.Kind)
	c.LLM.Provider = normalize(c.LLM.Provider)
	c.LLM.Reasoning.Level = strings.ToLower(strings.TrimSpace(c.LLM.Reasoning.Level))
	if c.LLM.Reasoning.Level == "" {
		c.LLM.Reasoning.Level = "medium"
	}
	if c.LLM.Reasoning.Enabled && c.LLM.Reasoning.Level == "off" {
		c.LLM.Reasoning.Level = "medium"
	}
	if !c.LLM.Reasoning.Enabled && c.LLM.Reasoning.Level != "" && c.LLM.Reasoning.Level != "off" {
		c.LLM.Reasoning.Enabled = true
	}
	c.FinalAction.Kind = normalize(c.FinalAction.Kind)
	c.Agent.LogLevel = normalize(c.Agent.LogLevel)

	switch c.TaskSource.Kind {
	case "stdin", "file", "api", "queue":
	default:
		return fmt.Errorf("unknown task_source.kind: %q (use stdin, file, api or queue)", c.TaskSource.Kind)
	}
	if c.TaskSource.Kind == "file" && c.TaskSource.Path == "" {
		return fmt.Errorf("task_source.kind=file requires 'path'")
	}
	if c.TaskSource.Kind == "queue" && c.TaskSource.Dir == "" {
		return fmt.Errorf("task_source.kind=queue requires 'dir'")
	}
	if c.TaskSource.Kind == "api" && c.TaskSource.URL == "" {
		return fmt.Errorf("task_source.kind=api requires 'url'")
	}

	switch c.Anchor.Kind {
	case "none":
	case "command":
		if c.Anchor.Command == "" {
			return fmt.Errorf("anchor.kind=command requires 'command'")
		}
	default:
		return fmt.Errorf("unknown anchor.kind: %q (use command or none)", c.Anchor.Kind)
	}

	switch c.Sandbox.Kind {
	case "none", "chroot", "cgroups":
	default:
		return fmt.Errorf("unknown sandbox.kind: %q (use none, chroot or cgroups)", c.Sandbox.Kind)
	}
	if c.Sandbox.Kind == "chroot" && c.Sandbox.Root == "" {
		return fmt.Errorf("sandbox.kind=chroot requires 'root' (the chroot root directory)")
	}
	switch c.Sandbox.Cgroups {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("unknown sandbox.cgroups: %q (use auto, on or off)", c.Sandbox.Cgroups)
	}
	if c.Sandbox.User != "" {
		if _, _, err := ParseUser(c.Sandbox.User); err != nil {
			return err
		}
	}

	switch c.LLM.Provider {
	case "openai", "ollama", "anthropic", "gemini":
	default:
		return fmt.Errorf("unknown llm.provider: %q (use openai, ollama, anthropic or gemini)", c.LLM.Provider)
	}
	if c.LLM.Model == "" {
		return fmt.Errorf("llm.model cannot be empty")
	}
	if requireKey && c.LLM.APIKey == "" {
		// Name the variable that really works for the configured provider: for
		// Ollama Cloud that is OLLAMA_API_KEY, and telling the user to export
		// OPENAI_API_KEY would send them to a name the loader ignores.
		return fmt.Errorf("the LLM key is missing: set llm.api_key in the YAML or %s", ProviderKeyVariable(c.LLM.Provider))
	}
	if c.LLM.MaxAttempts < 1 {
		return fmt.Errorf("llm.max_attempts must be >= 1")
	}
	if c.LLM.BackoffInitial <= 0 {
		return fmt.Errorf("llm.backoff_initial must be greater than zero")
	}
	if c.LLM.BackoffMax < c.LLM.BackoffInitial {
		return fmt.Errorf("llm.backoff_max cannot be smaller than llm.backoff_initial")
	}

	switch c.FinalAction.Kind {
	case "none", "command":
	case "api":
		if c.FinalAction.URL == "" {
			return fmt.Errorf("final_action.kind=api requires 'url'")
		}
	case "git_commit":
	default:
		return fmt.Errorf("unknown final_action.kind: %q (use none, command, api or git_commit)", c.FinalAction.Kind)
	}

	if c.Agent.MaxRetries < 0 {
		return fmt.Errorf("agent.max_retries cannot be negative")
	}
	if c.Agent.WorkspaceDir == "" {
		c.Agent.WorkspaceDir = Default().Agent.WorkspaceDir
	}
	switch c.Agent.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unknown agent.log_level: %q (use debug, info, warn or error)", c.Agent.LogLevel)
	}
	if c.Agent.LogBackups < 0 {
		return fmt.Errorf("agent.log_backups cannot be negative")
	}
	if c.Agent.OnFailure.Kind == "" {
		c.Agent.OnFailure.Kind = "none"
	}
	if c.Agent.OnFailure.Kind != "none" && c.Agent.OnFailure.Kind != "command" {
		return fmt.Errorf("unknown agent.on_failure.kind: %q (use none or command)", c.Agent.OnFailure.Kind)
	}
	return nil
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ParseUser interprets "uid:gid" (it also accepts just "uid").
func ParseUser(s string) (uid, gid int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) > 2 || parts[0] == "" {
		return 0, 0, fmt.Errorf("sandbox.user must have the uid:gid format, got %q", s)
	}
	uid, err = parseInteger(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("sandbox.user: invalid uid in %q: %w", s, err)
	}
	gid = uid
	if len(parts) == 2 {
		gid, err = parseInteger(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("sandbox.user: invalid gid in %q: %w", s, err)
		}
	}
	return uid, gid, nil
}

func parseInteger(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return n, nil
}
