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
}

// Template is a prompt with {{name}} variables.
type Template struct {
	System string `yaml:"system"`
	User   string `yaml:"user"`
}

// Prompts groups the three templates of the flow.
type Prompts struct {
	Analyze Template `yaml:"analyze"`
	Plan    Template `yaml:"plan"`
	Execute Template `yaml:"execute"`
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
	Shell     string    `yaml:"shell"`
	OnFailure OnFailure `yaml:"on_failure"`
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
		},
		Prompts: Prompts{
			Analyze: BaseAnalyzeTemplate,
			Plan:    BasePlanTemplate,
			Execute: BaseExecuteTemplate,
		},
		FinalAction: FinalAction{
			Kind:          "none",
			Method:        "POST",
			CommitMessage: "agent: {{task}}",
		},
		Agent: Agent{
			MaxRetries:      3,
			SubtaskDepth:    1,
			WorkspaceDir:    "./workspace",
			LogLevel:        "info",
			LogConsole:      true,
			LogMaxMB:        5,
			LogBackups:      3,
			ShutdownTimeout: 15 * time.Second,
			OnFailure:       OnFailure{Kind: "none"},
		},
	}
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// Load reads the YAML, applies the missing default values, overlays the
// environment variables and validates the result, requiring the LLM key.
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
	}

	// Environment variables (they win over the YAML) and compatibility with the
	// standard OPENAI_* variables.
	if err := ApplyEnvironment(&cfg); err != nil {
		return cfg, err
	}
	if v := os.Getenv("OPENAI_API_KEY"); cfg.LLM.APIKey == "" && v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" && os.Getenv("STARLIGHT_LLM_BASE_URL") == "" && cfg.LLM.BaseURL == Default().LLM.BaseURL {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" && os.Getenv("STARLIGHT_LLM_MODEL") == "" && cfg.LLM.Model == Default().LLM.Model {
		cfg.LLM.Model = v
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
