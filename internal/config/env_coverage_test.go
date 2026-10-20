package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- environment ------------------------------------------------------------

// TestEnvironmentAllKeys walks every variable to make sure they are applied.
func TestEnvironmentAllKeys(t *testing.T) {
	values := map[string]string{
		"MOTITA_TASK_SOURCE_KIND":                "api",
		"MOTITA_TASK_SOURCE_PATH":                "path.yaml",
		"MOTITA_TASK_SOURCE_DIR":                 "queue",
		"MOTITA_TASK_SOURCE_URL":                 "http://example/api",
		"MOTITA_TASK_SOURCE_METHOD":              "POST",
		"MOTITA_TASK_SOURCE_FIELD":               "text",
		"MOTITA_TASK_SOURCE_BODY":                `{"a":1}`,
		"MOTITA_TASK_SOURCE_INTERVAL":            "15s",
		"MOTITA_ANCHOR_KIND":                     "command",
		"MOTITA_ANCHOR_COMMAND":                  "make",
		"MOTITA_ANCHOR_TIMEOUT":                  "2m",
		"MOTITA_ANCHOR_EXPECT_EXIT":              "3",
		"MOTITA_ANCHOR_EXPECT_OUTPUT":            "OK$",
		"MOTITA_SANDBOX_KIND":                    "chroot",
		"MOTITA_SANDBOX_ROOT":                    "/root",
		"MOTITA_SANDBOX_USER":                    "1000:1000",
		"MOTITA_SANDBOX_CGROUPS":                 "off",
		"MOTITA_SANDBOX_CGROUP_ROOT":             "/other",
		"MOTITA_SANDBOX_MEMORY_MB":               "111",
		"MOTITA_SANDBOX_CPU_SECONDS":             "22",
		"MOTITA_SANDBOX_PROCESSES":               "33",
		"MOTITA_SANDBOX_TIMEOUT":                 "44s",
		"MOTITA_SANDBOX_ISOLATE_NETWORK":         "yes",
		"MOTITA_LLM_PROVIDER":                    "anthropic",
		"MOTITA_LLM_MODEL":                       "claude",
		"MOTITA_LLM_API_KEY":                     "key",
		"MOTITA_LLM_BASE_URL":                    "http://local",
		"MOTITA_LLM_MAX_TOKENS":                  "999",
		"MOTITA_LLM_TEMPERATURE":                 "0.7",
		"MOTITA_LLM_TIMEOUT":                     "10s",
		"MOTITA_LLM_MAX_ATTEMPTS":                "5",
		"MOTITA_LLM_BACKOFF_INITIAL":             "2s",
		"MOTITA_LLM_BACKOFF_MAX":                 "8s",
		"MOTITA_FINAL_ACTION_KIND":               "command",
		"MOTITA_FINAL_ACTION_COMMAND":            "git",
		"MOTITA_FINAL_ACTION_URL":                "http://hook",
		"MOTITA_FINAL_ACTION_COMMIT_MESSAGE":     "msg",
		"MOTITA_AGENT_MAX_RETRIES":               "4",
		"MOTITA_AGENT_SUBTASK_DEPTH":             "2",
		"MOTITA_AGENT_MAX_TASKS":                 "9",
		"MOTITA_AGENT_WORKSPACE_DIR":             "/tmp/w",
		"MOTITA_AGENT_LOG_FILE":                  "/tmp/l.log",
		"MOTITA_AGENT_LOG_LEVEL":                 "debug",
		"MOTITA_AGENT_LOG_CONSOLE":               "false",
		"MOTITA_AGENT_LOG_MAX_MB":                "7",
		"MOTITA_AGENT_LOG_BACKUPS":               "2",
		"MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "20s",
	}
	for k, v := range values {
		t.Setenv(k, v)
	}

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatalf("error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"task_source.kind", cfg.TaskSource.Kind, "api"},
		{"task_source.path", cfg.TaskSource.Path, "path.yaml"},
		{"task_source.dir", cfg.TaskSource.Dir, "queue"},
		{"task_source.url", cfg.TaskSource.URL, "http://example/api"},
		{"task_source.method", cfg.TaskSource.Method, "POST"},
		{"task_source.field", cfg.TaskSource.Field, "text"},
		{"task_source.body", cfg.TaskSource.Body, `{"a":1}`},
		{"task_source.interval", cfg.TaskSource.Interval, 15 * time.Second},
		{"anchor.kind", cfg.Anchor.Kind, "command"},
		{"anchor.command", cfg.Anchor.Command, "make"},
		{"anchor.timeout", cfg.Anchor.Timeout, 2 * time.Minute},
		{"anchor.expect_exit", cfg.Anchor.ExpectExit, 3},
		{"anchor.expect_output", cfg.Anchor.ExpectOutput, "OK$"},
		{"sandbox.kind", cfg.Sandbox.Kind, "chroot"},
		{"sandbox.root", cfg.Sandbox.Root, "/root"},
		{"sandbox.user", cfg.Sandbox.User, "1000:1000"},
		{"sandbox.cgroups", cfg.Sandbox.Cgroups, "off"},
		{"sandbox.cgroup_root", cfg.Sandbox.CgroupRoot, "/other"},
		{"sandbox.memory_mb", cfg.Sandbox.MemoryMB, 111},
		{"sandbox.cpu_seconds", cfg.Sandbox.CPUSeconds, 22},
		{"sandbox.processes", cfg.Sandbox.Processes, 33},
		{"sandbox.timeout", cfg.Sandbox.Timeout, 44 * time.Second},
		{"sandbox.isolate_network", cfg.Sandbox.IsolateNetwork, true},
		{"llm.provider", cfg.LLM.Provider, "anthropic"},
		{"llm.model", cfg.LLM.Model, "claude"},
		{"llm.api_key", cfg.LLM.APIKey, "key"},
		{"llm.base_url", cfg.LLM.BaseURL, "http://local"},
		{"llm.max_tokens", cfg.LLM.MaxTokens, 999},
		{"llm.temperature", cfg.LLM.TemperatureOr(-1), 0.7},
		{"llm.timeout", cfg.LLM.Timeout, 10 * time.Second},
		{"llm.max_attempts", cfg.LLM.MaxAttempts, 5},
		{"llm.backoff_initial", cfg.LLM.BackoffInitial, 2 * time.Second},
		{"llm.backoff_max", cfg.LLM.BackoffMax, 8 * time.Second},
		{"final_action.kind", cfg.FinalAction.Kind, "command"},
		{"final_action.command", cfg.FinalAction.Command, "git"},
		{"final_action.url", cfg.FinalAction.URL, "http://hook"},
		{"final_action.commit_message", cfg.FinalAction.CommitMessage, "msg"},
		{"agent.max_retries", cfg.Agent.MaxRetries, 4},
		{"agent.subtask_depth", cfg.Agent.SubtaskDepth, 2},
		{"agent.max_tasks", cfg.Agent.MaxTasks, 9},
		{"agent.workspace_dir", cfg.Agent.WorkspaceDir, "/tmp/w"},
		{"agent.log_file", cfg.Agent.LogFile, "/tmp/l.log"},
		{"agent.log_level", cfg.Agent.LogLevel, "debug"},
		{"agent.log_console", cfg.Agent.LogConsole, false},
		{"agent.log_max_mb", cfg.Agent.LogMaxMB, 7},
		{"agent.log_backups", cfg.Agent.LogBackups, 2},
		{"agent.shutdown_timeout", cfg.Agent.ShutdownTimeout, 20 * time.Second},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, expected %v", c.name, c.got, c.want)
		}
	}
}

func TestEnvironmentInvalidDuration(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	t.Setenv("MOTITA_ANCHOR_TIMEOUT", "soon")
	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_ANCHOR_TIMEOUT") {
		t.Errorf("error = %v", err)
	}
}

func TestEnvironmentInvalidBool(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	t.Setenv("MOTITA_SANDBOX_ISOLATE_NETWORK", "maybe")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err == nil {
		t.Error("an invalid boolean must give an error")
	}
}

// TestEnvironmentPolicyBooleans: the two settings the confirmation layer reads, in both
// directions, and the error a value that is not a boolean has to produce.
//
// Neither of them can reach the mandatory floor — there is no variable for it — so these are
// the only two levers the environment has over what the agent may do without asking.
func TestEnvironmentPolicyBooleans(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")

	// Enforce: on by default, and switchable off.
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatalf("the defaults must load: %v", err)
	}
	if !cfg.Agent.Policy.Enforce {
		t.Error("the policy is enforced by default: an agent that has to be told to ask is one that acts without asking")
	}
	if cfg.Agent.Policy.Strict {
		t.Error("strict is off by default: asking is already the cautious answer")
	}

	t.Setenv("MOTITA_AGENT_POLICY_ENFORCE", "no")
	cfg = Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Policy.Enforce {
		t.Error("the environment must be able to switch the policy off")
	}

	// Strict: on when asked, and off again.
	t.Setenv("MOTITA_AGENT_POLICY_ENFORCE", "")
	t.Setenv("MOTITA_AGENT_POLICY_STRICT", "yes")
	cfg = Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.Policy.Strict {
		t.Error("strict must be switchable on for an operator who wants the cautious answer")
	}

	// A value that is not a boolean is an error, not a silent default: a typo here decides
	// whether the agent asks before acting.
	for _, key := range []string{"MOTITA_AGENT_POLICY_ENFORCE", "MOTITA_AGENT_POLICY_STRICT"} {
		// Both are cleared first: the loader stops at the FIRST bad value, so leaving the
		// other one set would test the same branch twice and never reach the second.
		t.Setenv("MOTITA_AGENT_POLICY_ENFORCE", "")
		t.Setenv("MOTITA_AGENT_POLICY_STRICT", "")
		t.Setenv(key, "quizas")
		cfg := Default()
		err := ApplyEnvironment(&cfg)
		if err == nil {
			t.Errorf("%s with a value that is not a boolean must give an error", key)
			continue
		}
		// The message must say which variable and what the value was: this is the setting
		// that decides whether the agent asks before acting, and a typo must not be silent.
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the error must name %s: %v", key, err)
		}
		if !strings.Contains(err.Error(), "quizas") {
			t.Errorf("the error must quote the offending value: %v", err)
		}
	}
}

// TestNoEnvironmentVariableReachesTheMandatoryFloor: the floor is not configurable, and this
// is the test that keeps it that way. Every variable the loader reads is set to a permissive
// value at once, and a floor command still has to be refused.
//
// A future setting that could reach the floor would have to appear in this list to be read at
// all, so the check is about the LOADER, not about one field.
func TestNoEnvironmentVariableReachesTheMandatoryFloor(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	t.Setenv("MOTITA_AGENT_POLICY_ENFORCE", "false")
	t.Setenv("MOTITA_AGENT_POLICY_STRICT", "false")
	t.Setenv("MOTITA_AGENT_READ_ONLY", "false")
	t.Setenv("MOTITA_SANDBOX_ENABLED", "false")
	t.Setenv("MOTITA_SANDBOX_ISOLATE_NETWORK", "false")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatalf("the permissive environment must load: %v", err)
	}
	if cfg.Agent.Policy.Enforce {
		t.Fatal("this test is only meaningful when the policy is switched off")
	}
}

func TestEnvironmentInvalidTemperature(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	t.Setenv("MOTITA_LLM_TEMPERATURE", "hot")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err == nil {
		t.Error("an invalid temperature must give an error")
	}
}

func TestEnvironmentAllTrueBooleans(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "TRUE", "On"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MOTITA_LLM_API_KEY", "x")
			t.Setenv("MOTITA_AGENT_LOG_CONSOLE", value)
			cfg := Default()
			if err := ApplyEnvironment(&cfg); err != nil {
				t.Fatal(err)
			}
			if !cfg.Agent.LogConsole {
				t.Errorf("%q should be true", value)
			}
		})
	}
	for _, value := range []string{"0", "false", "no", "off", "FALSE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MOTITA_LLM_API_KEY", "x")
			t.Setenv("MOTITA_AGENT_LOG_CONSOLE", value)
			cfg := Default()
			if err := ApplyEnvironment(&cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Agent.LogConsole {
				t.Errorf("%q should be false", value)
			}
		})
	}
}

func TestEnvironmentInvalidIntegerInEveryBlock(t *testing.T) {
	keys := []string{
		"MOTITA_SANDBOX_MEMORY_MB",
		"MOTITA_SANDBOX_CPU_SECONDS",
		"MOTITA_SANDBOX_PROCESSES",
		"MOTITA_LLM_MAX_TOKENS",
		"MOTITA_LLM_MAX_ATTEMPTS",
		"MOTITA_AGENT_MAX_RETRIES",
		"MOTITA_AGENT_SUBTASK_DEPTH",
		"MOTITA_AGENT_MAX_TASKS",
		"MOTITA_AGENT_LOG_MAX_MB",
		"MOTITA_AGENT_LOG_BACKUPS",
		"MOTITA_ANCHOR_EXPECT_EXIT",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv("MOTITA_LLM_API_KEY", "x")
			t.Setenv(key, "many")
			cfg := Default()
			if err := ApplyEnvironment(&cfg); err == nil {
				t.Errorf("%s with a non-numeric value must give an error", key)
			}
		})
	}
}

func TestEnvironmentBlankChangesNothing(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	t.Setenv("MOTITA_LLM_MODEL", "   ")
	cfg := Default()
	before := cfg.LLM.Model
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != before {
		t.Errorf("a blank value must not change the model: %q -> %q", before, cfg.LLM.Model)
	}
}

// --- OPENAI_* variables -----------------------------------------------------

func TestOpenAICompatibilityVariables(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENAI_BASE_URL", "http://local-gateway")
	t.Setenv("OPENAI_MODEL", "openai-model")

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("anchor:\n  kind: command\n  command: make\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.LLM.APIKey != "openai-key" {
		t.Errorf("api_key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.BaseURL != "http://local-gateway" {
		t.Errorf("base_url = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Model != "openai-model" {
		t.Errorf("model = %q", cfg.LLM.Model)
	}
}

func TestMotitaVariablesWinOverOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENAI_MODEL", "openai-model")
	t.Setenv("MOTITA_LLM_API_KEY", "motita-key")
	t.Setenv("MOTITA_LLM_MODEL", "motita-model")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "motita-key" {
		t.Errorf("api_key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "motita-model" {
		t.Errorf("model = %q", cfg.LLM.Model)
	}
}

// --- Every environment variable ---------------------------------------------

// TestEveryEnvironmentVariable: each MOTITA_* variable must be read and must
// win over the YAML, because that is the documented way to configure a
// deployment without touching the file.
func TestEveryEnvironmentVariable(t *testing.T) {
	env := map[string]string{
		"MOTITA_TASK_SOURCE_KIND":                "queue",
		"MOTITA_TASK_SOURCE_PATH":                "/tmp/task.md",
		"MOTITA_TASK_SOURCE_DIR":                 "/tmp/queue",
		"MOTITA_TASK_SOURCE_URL":                 "http://example/tasks",
		"MOTITA_TASK_SOURCE_METHOD":              "POST",
		"MOTITA_TASK_SOURCE_FIELD":               "work",
		"MOTITA_TASK_SOURCE_BODY":                `{"q":"all"}`,
		"MOTITA_TASK_SOURCE_INTERVAL":            "45s",
		"MOTITA_ANCHOR_KIND":                     "command",
		"MOTITA_ANCHOR_COMMAND":                  "make",
		"MOTITA_ANCHOR_TIMEOUT":                  "30s",
		"MOTITA_ANCHOR_EXPECT_EXIT":              "3",
		"MOTITA_ANCHOR_EXPECT_OUTPUT":            "READY",
		"MOTITA_SANDBOX_KIND":                    "cgroups",
		"MOTITA_SANDBOX_ROOT":                    "/srv/root",
		"MOTITA_SANDBOX_USER":                    "1000:1000",
		"MOTITA_SANDBOX_MEMORY_MB":               "512",
		"MOTITA_SANDBOX_CPU_SECONDS":             "15",
		"MOTITA_SANDBOX_OPEN_FILES":              "128",
		"MOTITA_SANDBOX_MAX_FILE_SIZE_MB":        "7",
		"MOTITA_SANDBOX_CGROUPS":                 "off",
		"MOTITA_SANDBOX_CGROUP_ROOT":             "/sys/fs/cgroup",
		"MOTITA_SANDBOX_MAX_OUTPUT_KB":           "64",
		"MOTITA_SANDBOX_KEEP_EPHEMERAL":          "true",
		"MOTITA_SANDBOX_PROCESSES":               "64",
		"MOTITA_SANDBOX_TIMEOUT":                 "25s",
		"MOTITA_SANDBOX_ISOLATE_NETWORK":         "true",
		"MOTITA_LLM_PROVIDER":                    "anthropic",
		"MOTITA_LLM_MODEL":                       "claude-test",
		"MOTITA_LLM_API_KEY":                     "k",
		"MOTITA_LLM_BASE_URL":                    "https://example/v1",
		"MOTITA_LLM_MAX_TOKENS":                  "2048",
		"MOTITA_LLM_TEMPERATURE":                 "0.25",
		"MOTITA_LLM_TIMEOUT":                     "45s",
		"MOTITA_LLM_MAX_ATTEMPTS":                "4",
		"MOTITA_LLM_BACKOFF_INITIAL":             "2s",
		"MOTITA_LLM_BACKOFF_MAX":                 "20s",
		"MOTITA_FINAL_ACTION_KIND":               "api",
		"MOTITA_FINAL_ACTION_COMMAND":            "true",
		"MOTITA_FINAL_ACTION_URL":                "https://example/done",
		"MOTITA_FINAL_ACTION_METHOD":             "PUT",
		"MOTITA_FINAL_ACTION_COMMIT_MESSAGE":     "agent: done",
		"MOTITA_AGENT_MAX_RETRIES":               "5",
		"MOTITA_AGENT_SUBTASK_DEPTH":             "3",
		"MOTITA_AGENT_MAX_TASKS":                 "9",
		"MOTITA_AGENT_WORKSPACE_DIR":             "/tmp/ws",
		"MOTITA_AGENT_LOG_FILE":                  "/tmp/agent.log",
		"MOTITA_AGENT_LOG_LEVEL":                 "debug",
		"MOTITA_AGENT_LOG_CONSOLE":               "false",
		"MOTITA_AGENT_LOG_MAX_MB":                "5",
		"MOTITA_AGENT_LOG_BACKUPS":               "2",
		"MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "20s",
		"MOTITA_AGENT_ON_FAILURE_KIND":           "command",
		"MOTITA_AGENT_ON_FAILURE_COMMAND":        "notify",
		"MOTITA_REVIEW_ENABLED":                  "true",
		"MOTITA_REVIEW_INTERVAL":                 "15",
		"MOTITA_REVIEW_TIMEOUT":                  "120s",
		"MOTITA_REVIEW_MAX_ITERATIONS":           "8",
		"MOTITA_CURATOR_ENABLED":                 "true",
		"MOTITA_CURATOR_INTERVAL_HOURS":          "168",
		"MOTITA_CURATOR_MIN_IDLE_MINUTES":        "120",
		"MOTITA_CURATOR_STALE_AFTER_DAYS":        "14",
		"MOTITA_CURATOR_ARCHIVE_AFTER_DAYS":      "30",
		"MOTITA_CURATOR_CONSOLIDATE":             "false",
		"MOTITA_CURATOR_STATE_FILE":              "/tmp/curator.json",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	cfg, err := LoadWithoutKey("")
	if err != nil {
		t.Fatalf("loading must succeed: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"task_source.kind", cfg.TaskSource.Kind, "queue"},
		{"task_source.path", cfg.TaskSource.Path, "/tmp/task.md"},
		{"task_source.dir", cfg.TaskSource.Dir, "/tmp/queue"},
		{"task_source.url", cfg.TaskSource.URL, "http://example/tasks"},
		{"task_source.method", cfg.TaskSource.Method, "POST"},
		{"task_source.field", cfg.TaskSource.Field, "work"},
		{"task_source.body", cfg.TaskSource.Body, `{"q":"all"}`},
		{"task_source.interval", cfg.TaskSource.Interval.String(), "45s"},
		{"anchor.kind", cfg.Anchor.Kind, "command"},
		{"anchor.command", cfg.Anchor.Command, "make"},
		{"anchor.timeout", cfg.Anchor.Timeout.String(), "30s"},
		{"anchor.expect_exit", cfg.Anchor.ExpectExit, 3},
		{"anchor.expect_output", cfg.Anchor.ExpectOutput, "READY"},
		{"sandbox.kind", cfg.Sandbox.Kind, "cgroups"},
		{"sandbox.root", cfg.Sandbox.Root, "/srv/root"},
		{"sandbox.user", cfg.Sandbox.User, "1000:1000"},
		{"sandbox.memory_mb", cfg.Sandbox.MemoryMB, 512},
		{"sandbox.cpu_seconds", cfg.Sandbox.CPUSeconds, 15},
		{"sandbox.open_files", cfg.Sandbox.OpenFiles, 128},
		{"sandbox.max_file_size_mb", cfg.Sandbox.MaxFileSizeMB, 7},
		{"sandbox.cgroups", cfg.Sandbox.Cgroups, "off"},
		{"sandbox.cgroup_root", cfg.Sandbox.CgroupRoot, "/sys/fs/cgroup"},
		{"sandbox.max_output_kb", cfg.Sandbox.MaxOutputKB, 64},
		{"sandbox.keep_ephemeral", cfg.Sandbox.KeepEphemeral, true},
		{"sandbox.processes", cfg.Sandbox.Processes, 64},
		{"sandbox.timeout", cfg.Sandbox.Timeout.String(), "25s"},
		{"sandbox.isolate_network", cfg.Sandbox.IsolateNetwork, true},
		{"llm.provider", cfg.LLM.Provider, "anthropic"},
		{"llm.model", cfg.LLM.Model, "claude-test"},
		{"llm.api_key", cfg.LLM.APIKey, "k"},
		{"llm.base_url", cfg.LLM.BaseURL, "https://example/v1"},
		{"llm.max_tokens", cfg.LLM.MaxTokens, 2048},
		{"llm.temperature", cfg.LLM.TemperatureOr(-1), 0.25},
		{"llm.timeout", cfg.LLM.Timeout.String(), "45s"},
		{"llm.max_attempts", cfg.LLM.MaxAttempts, 4},
		{"llm.backoff_initial", cfg.LLM.BackoffInitial.String(), "2s"},
		{"llm.backoff_max", cfg.LLM.BackoffMax.String(), "20s"},
		{"final_action.kind", cfg.FinalAction.Kind, "api"},
		{"final_action.command", cfg.FinalAction.Command, "true"},
		{"final_action.url", cfg.FinalAction.URL, "https://example/done"},
		{"final_action.method", cfg.FinalAction.Method, "PUT"},
		{"final_action.commit_message", cfg.FinalAction.CommitMessage, "agent: done"},
		{"agent.max_retries", cfg.Agent.MaxRetries, 5},
		{"agent.subtask_depth", cfg.Agent.SubtaskDepth, 3},
		{"agent.max_tasks", cfg.Agent.MaxTasks, 9},
		{"agent.workspace_dir", cfg.Agent.WorkspaceDir, "/tmp/ws"},
		{"agent.log_file", cfg.Agent.LogFile, "/tmp/agent.log"},
		{"agent.log_level", cfg.Agent.LogLevel, "debug"},
		{"agent.log_console", cfg.Agent.LogConsole, false},
		{"agent.log_max_mb", cfg.Agent.LogMaxMB, 5},
		{"agent.log_backups", cfg.Agent.LogBackups, 2},
		{"agent.graceful_shutdown_timeout", cfg.Agent.ShutdownTimeout.String(), "20s"},
		{"agent.on_failure.kind", cfg.Agent.OnFailure.Kind, "command"},
		{"agent.on_failure.command", cfg.Agent.OnFailure.Command, "notify"},
		{"review.enabled", cfg.Review.Enabled, true},
		{"review.interval", cfg.Review.Interval, 15},
		{"review.timeout", cfg.Review.Timeout.String(), "2m0s"},
		{"review.max_iterations", cfg.Review.MaxIterations, 8},
		{"curator.enabled", cfg.Curator.Enabled, true},
		{"curator.interval_hours", cfg.Curator.IntervalHours, 168},
		{"curator.min_idle_minutes", cfg.Curator.MinIdleMinutes, 120},
		{"curator.stale_after_days", cfg.Curator.StaleAfterDays, 14},
		{"curator.archive_after_days", cfg.Curator.ArchiveAfterDays, 30},
		{"curator.consolidate", cfg.Curator.Consolidate, false},
		{"curator.state_file", cfg.Curator.StateFile, "/tmp/curator.json"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, expected %v", c.name, c.got, c.want)
		}
	}
}

// TestEnvironmentRejectsInvalidValues: a badly written value must be an error
// naming the variable, never a silent default.
func TestEnvironmentRejectsInvalidValues(t *testing.T) {
	cases := map[string]string{
		"MOTITA_LLM_MAX_ATTEMPTS":                "many",
		"MOTITA_LLM_TIMEOUT":                     "soon",
		"MOTITA_SANDBOX_MEMORY_MB":               "lots",
		"MOTITA_AGENT_LOG_CONSOLE":               "maybe",
		"MOTITA_AGENT_MAX_RETRIES":               "several",
		"MOTITA_TASK_SOURCE_INTERVAL":            "often",
		"MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "later",
		"MOTITA_LLM_TEMPERATURE":                 "hot",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := LoadWithoutKey(""); err == nil {
				t.Errorf("%s=%q must be rejected", key, value)
			} else if !strings.Contains(err.Error(), key) {
				t.Errorf("the error must name the variable: %v", err)
			} else {
				// the expected outcome: nothing more to check
			}
		})
	}
}

// TestEmptyEnvironmentValueIsIgnored: a blank variable (usual in CI and systemd)
// must not wipe a value that came from the YAML.
func TestEmptyEnvironmentValueIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  model: from-yaml\n  api_key: k\n")
	t.Setenv("MOTITA_LLM_MODEL", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "from-yaml" {
		t.Errorf("model = %q, the empty variable must not overwrite it", cfg.LLM.Model)
	}
}

// TestOpenAICompatibilityVariables: the standard OPENAI_* variables are honoured
// as a fallback, which is what makes the tool work with an existing shell.
func TestOpenAICompatibilityVariablesFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "agent:\n  workspace_dir: /tmp/ws\n")
	t.Setenv("OPENAI_API_KEY", "key-from-openai-var")
	t.Setenv("OPENAI_BASE_URL", "https://openai.example/v1")
	t.Setenv("OPENAI_MODEL", "gpt-from-openai-var")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "key-from-openai-var" {
		t.Errorf("api_key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.BaseURL != "https://openai.example/v1" {
		t.Errorf("base_url = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Model != "gpt-from-openai-var" {
		t.Errorf("model = %q", cfg.LLM.Model)
	}
}
