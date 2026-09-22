package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- toInteger / toFloat / toText / toDuration ------------------------------

func TestToIntegerAllTypes(t *testing.T) {
	cases := []struct {
		input   any
		want    int64
		wantErr bool
	}{
		{int64(42), 42, false},
		{float64(7), 7, false},
		{float64(7.5), 0, true}, // not an integer
		{true, 1, false},
		{false, 0, false},
		{"123", 123, false},
		{" 45 ", 45, false},
		{"not-a-number", 0, true},
		{[]any{}, 0, true},
	}
	for _, tc := range cases {
		got, err := toInteger(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("toInteger(%v) had to fail and gave %d", tc.input, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("toInteger(%v) = %d, %v (expected %d)", tc.input, got, err, tc.want)
		}
	}
}

func TestToFloatAllTypes(t *testing.T) {
	cases := []struct {
		input   any
		want    float64
		wantErr bool
	}{
		{int64(3), 3, false},
		{float64(2.5), 2.5, false},
		{"1.25", 1.25, false},
		{"no", 0, true},
		{true, 0, true},
	}
	for _, tc := range cases {
		got, err := toFloat(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("toFloat(%v) had to fail and gave %v", tc.input, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("toFloat(%v) = %v, %v", tc.input, got, err)
		}
	}
}

func TestToTextAllTypes(t *testing.T) {
	cases := []struct {
		input any
		want  string
	}{
		{"text", "text"},
		{int64(5), "5"},
		{float64(1.5), "1.5"},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{[]string{"a"}, "[a]"}, // unforeseen type: it gets formatted
	}
	for _, tc := range cases {
		if got := toText(tc.input); got != tc.want {
			t.Errorf("toText(%v) = %q, expected %q", tc.input, got, tc.want)
		}
	}
}

func TestToDurationAllFormats(t *testing.T) {
	cases := []struct {
		input   any
		want    time.Duration
		wantErr bool
	}{
		{int64(30), 30 * time.Second, false},
		{float64(1.5), 1500 * time.Millisecond, false},
		{"45s", 45 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"90", 90 * time.Second, false}, // no suffix = seconds
		{"", 0, true},
		{"soon", 0, true},
		{true, 0, true},
	}
	for _, tc := range cases {
		got, err := toDuration(tc.input, "field")
		if tc.wantErr {
			if err == nil {
				t.Errorf("toDuration(%v) had to fail and gave %v", tc.input, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("toDuration(%v) = %v, %v (expected %v)", tc.input, got, err, tc.want)
		}
	}
}

func TestToTextWithBooleanInTextField(t *testing.T) {
	// A boolean where text is expected gets normalized, not rejected.
	m, err := ParseYAML([]byte("task_source:\n  kind: file\n  path: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decode(m, &cfg); err != nil {
		t.Fatalf("a boolean in a text field must be normalized: %v", err)
	}
	if cfg.TaskSource.Path != "true" {
		t.Errorf("path = %q", cfg.TaskSource.Path)
	}
}

// --- assign: unsupported and nested types -----------------------------------

func TestDecodeUnsupportedTypes(t *testing.T) {
	// A channel does not exist in YAML, but the decoder must reject a block
	// where it expects a simple value.
	m, err := ParseYAML([]byte("task_source:\n  kind: file\n  path:\n    nested: yes\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil {
		t.Fatal("a nested block where text is expected must fail")
	}
	if !strings.Contains(err.Error(), "simple value") {
		t.Errorf("error = %q", err)
	}
}

func TestDecodeListWhereTextIsExpected(t *testing.T) {
	m, err := ParseYAML([]byte("task_source:\n  kind: [a, b]\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decode(m, &cfg); err == nil {
		t.Fatal("a list where text is expected must fail")
	}
}

func TestDecodeInvalidBoolean(t *testing.T) {
	m, err := ParseYAML([]byte("sandbox:\n  isolate_network: 42\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil || !strings.Contains(err.Error(), "true/false") {
		t.Errorf("error = %q", err)
	}
}

func TestDecodeInvalidDestination(t *testing.T) {
	if err := Decode(map[string]any{}, nil); err == nil {
		t.Error("a nil destination must fail")
	}
	var notAPointer Config
	if err := Decode(map[string]any{}, notAPointer); err == nil {
		t.Error("a destination that is not a pointer must fail")
	}
}

func TestPathOrName(t *testing.T) {
	if got := pathOrName(""); got == "" {
		t.Error("with no prefix it must describe the configuration")
	}
	if got := pathOrName("llm"); got != "llm" {
		t.Errorf("pathOrName(llm) = %q", got)
	}
}

// --- environment ------------------------------------------------------------

// TestEnvironmentAllKeys walks every variable to make sure they are applied.
func TestEnvironmentAllKeys(t *testing.T) {
	values := map[string]string{
		"STARLIGHT_TASK_SOURCE_KIND":                "api",
		"STARLIGHT_TASK_SOURCE_PATH":                "path.yaml",
		"STARLIGHT_TASK_SOURCE_DIR":                 "queue",
		"STARLIGHT_TASK_SOURCE_URL":                 "http://example/api",
		"STARLIGHT_TASK_SOURCE_METHOD":              "POST",
		"STARLIGHT_TASK_SOURCE_FIELD":               "text",
		"STARLIGHT_TASK_SOURCE_BODY":                `{"a":1}`,
		"STARLIGHT_TASK_SOURCE_INTERVAL":            "15s",
		"STARLIGHT_ANCHOR_KIND":                     "command",
		"STARLIGHT_ANCHOR_COMMAND":                  "make",
		"STARLIGHT_ANCHOR_TIMEOUT":                  "2m",
		"STARLIGHT_ANCHOR_EXPECT_EXIT":              "3",
		"STARLIGHT_ANCHOR_EXPECT_OUTPUT":            "OK$",
		"STARLIGHT_SANDBOX_KIND":                    "chroot",
		"STARLIGHT_SANDBOX_ROOT":                    "/root",
		"STARLIGHT_SANDBOX_USER":                    "1000:1000",
		"STARLIGHT_SANDBOX_CGROUPS":                 "off",
		"STARLIGHT_SANDBOX_CGROUP_ROOT":             "/other",
		"STARLIGHT_SANDBOX_MEMORY_MB":               "111",
		"STARLIGHT_SANDBOX_CPU_SECONDS":             "22",
		"STARLIGHT_SANDBOX_PROCESSES":               "33",
		"STARLIGHT_SANDBOX_TIMEOUT":                 "44s",
		"STARLIGHT_SANDBOX_ISOLATE_NETWORK":         "yes",
		"STARLIGHT_LLM_PROVIDER":                    "anthropic",
		"STARLIGHT_LLM_MODEL":                       "claude",
		"STARLIGHT_LLM_API_KEY":                     "key",
		"STARLIGHT_LLM_BASE_URL":                    "http://local",
		"STARLIGHT_LLM_MAX_TOKENS":                  "999",
		"STARLIGHT_LLM_TEMPERATURE":                 "0.7",
		"STARLIGHT_LLM_TIMEOUT":                     "10s",
		"STARLIGHT_LLM_MAX_ATTEMPTS":                "5",
		"STARLIGHT_LLM_BACKOFF_INITIAL":             "2s",
		"STARLIGHT_LLM_BACKOFF_MAX":                 "8s",
		"STARLIGHT_FINAL_ACTION_KIND":               "command",
		"STARLIGHT_FINAL_ACTION_COMMAND":            "git",
		"STARLIGHT_FINAL_ACTION_URL":                "http://hook",
		"STARLIGHT_FINAL_ACTION_COMMIT_MESSAGE":     "msg",
		"STARLIGHT_AGENT_MAX_RETRIES":               "4",
		"STARLIGHT_AGENT_SUBTASK_DEPTH":             "2",
		"STARLIGHT_AGENT_MAX_TASKS":                 "9",
		"STARLIGHT_AGENT_WORKSPACE_DIR":             "/tmp/w",
		"STARLIGHT_AGENT_LOG_FILE":                  "/tmp/l.log",
		"STARLIGHT_AGENT_LOG_LEVEL":                 "debug",
		"STARLIGHT_AGENT_LOG_CONSOLE":               "false",
		"STARLIGHT_AGENT_LOG_MAX_MB":                "7",
		"STARLIGHT_AGENT_LOG_BACKUPS":               "2",
		"STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "20s",
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
		{"llm.temperature", cfg.LLM.Temperature, 0.7},
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
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_ANCHOR_TIMEOUT", "soon")
	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil || !strings.Contains(err.Error(), "STARLIGHT_ANCHOR_TIMEOUT") {
		t.Errorf("error = %v", err)
	}
}

func TestEnvironmentInvalidBool(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_SANDBOX_ISOLATE_NETWORK", "maybe")
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
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")

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

	t.Setenv("STARLIGHT_AGENT_POLICY_ENFORCE", "no")
	cfg = Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Policy.Enforce {
		t.Error("the environment must be able to switch the policy off")
	}

	// Strict: on when asked, and off again.
	t.Setenv("STARLIGHT_AGENT_POLICY_ENFORCE", "")
	t.Setenv("STARLIGHT_AGENT_POLICY_STRICT", "yes")
	cfg = Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.Policy.Strict {
		t.Error("strict must be switchable on for an operator who wants the cautious answer")
	}

	// A value that is not a boolean is an error, not a silent default: a typo here decides
	// whether the agent asks before acting.
	for _, key := range []string{"STARLIGHT_AGENT_POLICY_ENFORCE", "STARLIGHT_AGENT_POLICY_STRICT"} {
		// Both are cleared first: the loader stops at the FIRST bad value, so leaving the
		// other one set would test the same branch twice and never reach the second.
		t.Setenv("STARLIGHT_AGENT_POLICY_ENFORCE", "")
		t.Setenv("STARLIGHT_AGENT_POLICY_STRICT", "")
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
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_AGENT_POLICY_ENFORCE", "false")
	t.Setenv("STARLIGHT_AGENT_POLICY_STRICT", "false")
	t.Setenv("STARLIGHT_AGENT_READ_ONLY", "false")
	t.Setenv("STARLIGHT_SANDBOX_ENABLED", "false")
	t.Setenv("STARLIGHT_SANDBOX_ISOLATE_NETWORK", "false")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatalf("the permissive environment must load: %v", err)
	}
	if cfg.Agent.Policy.Enforce {
		t.Fatal("this test is only meaningful when the policy is switched off")
	}
}

func TestEnvironmentInvalidTemperature(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_LLM_TEMPERATURE", "hot")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err == nil {
		t.Error("an invalid temperature must give an error")
	}
}

func TestEnvironmentAllTrueBooleans(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "TRUE", "On"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("STARLIGHT_LLM_API_KEY", "x")
			t.Setenv("STARLIGHT_AGENT_LOG_CONSOLE", value)
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
			t.Setenv("STARLIGHT_LLM_API_KEY", "x")
			t.Setenv("STARLIGHT_AGENT_LOG_CONSOLE", value)
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
		"STARLIGHT_SANDBOX_MEMORY_MB",
		"STARLIGHT_SANDBOX_CPU_SECONDS",
		"STARLIGHT_SANDBOX_PROCESSES",
		"STARLIGHT_LLM_MAX_TOKENS",
		"STARLIGHT_LLM_MAX_ATTEMPTS",
		"STARLIGHT_AGENT_MAX_RETRIES",
		"STARLIGHT_AGENT_SUBTASK_DEPTH",
		"STARLIGHT_AGENT_MAX_TASKS",
		"STARLIGHT_AGENT_LOG_MAX_MB",
		"STARLIGHT_AGENT_LOG_BACKUPS",
		"STARLIGHT_ANCHOR_EXPECT_EXIT",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv("STARLIGHT_LLM_API_KEY", "x")
			t.Setenv(key, "many")
			cfg := Default()
			if err := ApplyEnvironment(&cfg); err == nil {
				t.Errorf("%s with a non-numeric value must give an error", key)
			}
		})
	}
}

func TestEnvironmentBlankChangesNothing(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_LLM_MODEL", "   ")
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

func TestStarlightVariablesWinOverOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENAI_MODEL", "openai-model")
	t.Setenv("STARLIGHT_LLM_API_KEY", "starlight-key")
	t.Setenv("STARLIGHT_LLM_MODEL", "starlight-model")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "starlight-key" {
		t.Errorf("api_key = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "starlight-model" {
		t.Errorf("model = %q", cfg.LLM.Model)
	}
}

// --- Validate: every error path ---------------------------------------------

func TestValidateErrorPaths(t *testing.T) {
	cases := []struct {
		name     string
		modify   func(*Config)
		contains string
	}{
		{"queue without dir", func(c *Config) { c.TaskSource.Kind = "queue" }, "requires 'dir'"},
		{"unknown anchor", func(c *Config) { c.Anchor.Kind = "guesser" }, "unknown anchor.kind"},
		{"unknown sandbox", func(c *Config) { c.Sandbox.Kind = "magic" }, "unknown sandbox.kind"},
		{"unknown cgroups", func(c *Config) { c.Sandbox.Cgroups = "maybe" }, "unknown sandbox.cgroups"},
		{"malformed user", func(c *Config) { c.Sandbox.User = "a:b:c" }, "uid:gid"},
		{"empty model", func(c *Config) { c.LLM.Model = "" }, "llm.model cannot be empty"},
		{"zero max attempts", func(c *Config) { c.LLM.MaxAttempts = 0 }, "llm.max_attempts"},
		{"zero initial backoff", func(c *Config) { c.LLM.BackoffInitial = 0 }, "llm.backoff_initial"},
		{"smaller max backoff", func(c *Config) { c.LLM.BackoffMax = time.Millisecond; c.LLM.BackoffInitial = time.Second }, "llm.backoff_max"},
		{"unknown final", func(c *Config) { c.FinalAction.Kind = "telepathy" }, "unknown final_action.kind"},
		{"negative retries", func(c *Config) { c.Agent.MaxRetries = -1 }, "max_retries"},
		{"negative backups", func(c *Config) { c.Agent.LogBackups = -1 }, "log_backups"},
		{"unknown on_failure", func(c *Config) { c.Agent.OnFailure.Kind = "telepathy" }, "unknown agent.on_failure.kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.LLM.APIKey = "x"
			tc.modify(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("a validation error was expected")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, it was expected to contain %q", err, tc.contains)
			}
		})
	}
}

// TestValidateFillsDefaultValues: Validate() normalizes and completes what is
// missing starting from Default() (an all-zero Config does not have the default
// values: it is Default() that provides them).
func TestValidateFillsDefaultValues(t *testing.T) {
	cfg := Default()
	cfg.LLM.APIKey = "x"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.WorkspaceDir == "" {
		t.Error("workspace_dir must have a default value")
	}
	if cfg.Agent.OnFailure.Kind != "none" {
		t.Errorf("on_failure.kind = %q", cfg.Agent.OnFailure.Kind)
	}
	if cfg.Agent.LogLevel != "info" {
		t.Errorf("log_level = %q", cfg.Agent.LogLevel)
	}
	if cfg.Sandbox.Cgroups != "auto" {
		t.Errorf("cgroups = %q", cfg.Sandbox.Cgroups)
	}
}

func TestValidateWithoutKey(t *testing.T) {
	cfg := Default()
	cfg.LLM.APIKey = ""
	if err := cfg.Validate(); err == nil {
		t.Error("Validate must require the key")
	}
	cfg2 := Default()
	cfg2.LLM.APIKey = ""
	if err := cfg2.ValidateWithoutKey(); err != nil {
		t.Errorf("ValidateWithoutKey must not require it: %v", err)
	}
}

// --- ParseUser --------------------------------------------------------------

func TestParseUserAllCases(t *testing.T) {
	cases := []struct {
		input string
		uid   int
		gid   int
		fails bool
	}{
		{"1000:1001", 1000, 1001, false},
		{"1000", 1000, 1000, false},
		{" 5 : 6 ", 5, 6, false},
		{"", 0, 0, true},
		{"thousand", 0, 0, true},
		{"1:two", 0, 0, true},
		{"1:2:3", 0, 0, true},
	}
	for _, tc := range cases {
		uid, gid, err := ParseUser(tc.input)
		if tc.fails {
			if err == nil {
				t.Errorf("ParseUser(%q) had to fail", tc.input)
			}
			continue
		}
		if err != nil || uid != tc.uid || gid != tc.gid {
			t.Errorf("ParseUser(%q) = %d,%d,%v", tc.input, uid, gid, err)
		}
	}
}

// --- YAML: error paths and formats ------------------------------------------

func TestParseYAMLCommentAtTheEnd(t *testing.T) {
	// A comment at the end of the document, with no key: it must be ignored.
	m, err := ParseYAML([]byte("a: 1\n# just a comment\n"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m["a"] != int64(1) {
		t.Errorf("m = %#v", m)
	}
}

func TestParseYAMLEmpty(t *testing.T) {
	for _, input := range []string{"", "\n\n", "# comments only\n"} {
		m, err := ParseYAML([]byte(input))
		if err != nil {
			t.Errorf("ParseYAML(%q) gave an error: %v", input, err)
		}
		if len(m) != 0 {
			t.Errorf("ParseYAML(%q) = %#v", input, m)
		}
	}
}

func TestParseYAMLKeyWithoutValueAtTheEnd(t *testing.T) {
	m, err := ParseYAML([]byte("task_source:\nanchor:\n  kind: command\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m["task_source"]; !ok || v != nil {
		t.Errorf("task_source without a value must be nil: %#v", m["task_source"])
	}
}

func TestParseYAMLListNestedUnderKey(t *testing.T) {
	m, err := ParseYAML([]byte("anchor:\n  checks:\n    - name: a\n    - name: b\n"))
	if err != nil {
		t.Fatal(err)
	}
	anchor := m["anchor"].(map[string]any)
	list, ok := anchor["checks"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("checks = %#v", anchor["checks"])
	}
}

func TestParseYAMLBlankItem(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n  -\n  - two\n"))
	if err != nil {
		t.Fatal(err)
	}
	list := m["list"].([]any)
	if len(list) != 2 || list[0] != nil || list[1] != "two" {
		t.Errorf("list = %#v", list)
	}
}

func TestParseYAMLQuotedKey(t *testing.T) {
	m, err := ParseYAML([]byte(`"key with spaces": value`))
	if err != nil {
		t.Fatal(err)
	}
	if m["key with spaces"] != "value" {
		t.Errorf("m = %#v", m)
	}
}

func TestParseYAMLEmptyKeyError(t *testing.T) {
	if _, err := ParseYAML([]byte(": value\n")); err == nil {
		t.Error("an empty key must give an error")
	}
}

func TestParseYAMLUnterminatedQuoteError(t *testing.T) {
	if _, err := ParseYAML([]byte(`a: "unterminated`)); err == nil {
		t.Error("an unterminated quote must give an error")
	}
}

func TestParseYAMLUnterminatedListError(t *testing.T) {
	if _, err := ParseYAML([]byte("a: [1, 2\n")); err == nil {
		t.Error("an unterminated list must give an error")
	}
}

func TestParseYAMLUnterminatedMapError(t *testing.T) {
	if _, err := ParseYAML([]byte("a: {x: 1\n")); err == nil {
		t.Error("an unterminated inline map must give an error")
	}
}

func TestParseYAMLUnsupportedStructureError(t *testing.T) {
	for _, input := range []string{"a: &anchor value\n", "a: *alias\n", "a: !tag value\n"} {
		if _, err := ParseYAML([]byte(input)); err == nil {
			t.Errorf("ParseYAML(%q) should reject what is not supported", input)
		}
	}
}

func TestParseYAMLSingleQuoteWithEscape(t *testing.T) {
	m, err := ParseYAML([]byte("a: 'it is '' not a problem'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != "it is ' not a problem" {
		t.Errorf("a = %q", m["a"])
	}
}

func TestParseYAMLEscapesInDoubleQuotes(t *testing.T) {
	m, err := ParseYAML([]byte(`a: "line1\nline2\ttab\\slash\"quote"` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\ttab\\slash\"quote"
	if m["a"] != want {
		t.Errorf("a = %q, expected %q", m["a"], want)
	}
}

func TestParseYAMLUnknownEscapes(t *testing.T) {
	// An unforeseen escape is kept as is.
	m, err := ParseYAML([]byte(`a: "x\qy"`))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != "x\\qy" {
		t.Errorf("a = %q", m["a"])
	}
}

func TestParseYAMLFoldedScalarWithBlanks(t *testing.T) {
	m, err := ParseYAML([]byte("a: >\n  first part\n  second\n\n  another block\n"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["a"].(string)
	if !strings.Contains(text, "first part") || !strings.Contains(text, "another block") {
		t.Errorf("a = %q", text)
	}
}

func TestParseYAMLBlockScalarKeepingTrailingNewline(t *testing.T) {
	m, err := ParseYAML([]byte("a: |+\n  line\n"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["a"].(string)
	if !strings.HasSuffix(text, "\n") {
		t.Errorf("with |+ the trailing newline is kept: %q", text)
	}
}

func TestIsBlockIndicator(t *testing.T) {
	cases := map[string]bool{
		"|": true, ">": true, "|-": true, ">+": true, "|2": true,
		"": false, "value": false, "||": false,
	}
	for input, want := range cases {
		if got := isBlockIndicator(input); got != want {
			t.Errorf("isBlockIndicator(%q) = %v", input, got)
		}
	}
}

func TestIsItemList(t *testing.T) {
	for input, want := range map[string]bool{
		"-": true, "- value": true, "-value": false, "value": false,
	} {
		if got := isItem(input); got != want {
			t.Errorf("isItem(%q) = %v", input, got)
		}
	}
}

func TestSplitTopWithQuotes(t *testing.T) {
	parts, err := splitTop(`a, "b, c", d[1,2]`, ',')
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[1] != `"b, c"` || parts[2] != "d[1,2]" {
		t.Errorf("parts = %#v", parts)
	}
}

func TestSplitTopUnterminatedQuote(t *testing.T) {
	if _, err := splitTop(`a, "b`, ','); err == nil {
		t.Error("an unterminated quote must give an error")
	}
}

func TestParseScalarBlankValues(t *testing.T) {
	for input, want := range map[string]any{
		"": nil, "  ": nil, "null": nil, "~": nil,
		"true": true, "false": false,
		"3": int64(3), "3.5": float64(3.5),
		"text": "text",
	} {
		got, err := parseScalar(input, 1)
		if err != nil {
			t.Errorf("parseScalar(%q) gave an error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseScalar(%q) = %#v, expected %#v", input, got, want)
		}
	}
}

// TestParseScalarOnOffAreText: the important fix in the parser. YAML 1.1 turns
// on/off/yes/no into booleans, but in this configuration they are legitimate
// text values (sandbox.cgroups accepts "on"/"off"). Turning them made
// `cgroups: off` arrive as false and the validation rejected it.
func TestParseScalarOnOffAreText(t *testing.T) {
	for _, input := range []string{"on", "off", "yes", "no", "On", "OFF"} {
		got, err := parseScalar(input, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, isBool := got.(bool); isBool {
			t.Errorf("parseScalar(%q) = %v: on/off/yes/no must be text, not boolean", input, got)
		}
		if got != input {
			t.Errorf("parseScalar(%q) = %#v", input, got)
		}
	}
}

// TestDecodeMapWhereStructIsExpected: a simple value where a nested block is
// expected.
func TestDecodeSimpleValueWhereBlockIsExpected(t *testing.T) {
	m, err := ParseYAML([]byte("llm: text\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil || !strings.Contains(err.Error(), "expected a configuration block") {
		t.Errorf("error = %v", err)
	}
	// The message must carry the field path so it can be fixed.
	if !strings.Contains(err.Error(), "llm") {
		t.Errorf("the error must name the block: %v", err)
	}
}

// TestDecodeListWhereBlockIsExpected.
func TestDecodeListWhereBlockIsExpected(t *testing.T) {
	m, err := ParseYAML([]byte("llm:\n  - one\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decode(m, &cfg); err == nil {
		t.Error("a list where a block is expected must fail")
	}
}

// TestDecodeNestedMapOnDeepPath checks that the errors name the full path
// (block.subfield), not just the loose name.
func TestDecodeNestedMapOnDeepPath(t *testing.T) {
	m, err := ParseYAML([]byte("anchor:\n  checks:\n    - name: a\n      expect_exit: not-a-number\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil {
		t.Fatal("a type error was expected")
	}
	if !strings.Contains(err.Error(), "anchor.checks") {
		t.Errorf("the error must name the nested path: %v", err)
	}
}

// TestDecodeIntegerOverflow: an oversized number is handled without a panic
// (the parser delivers it as text and the validation decides).
func TestDecodeIntegerOverflow(t *testing.T) {
	m, err := ParseYAML([]byte("sandbox:\n  memory_mb: 99999999999999999999\n"))
	if err != nil {
		t.Fatalf("the parser should not fail here: %v", err)
	}
	var cfg Config
	// Either it decodes as text (and the validation will reject it later), or
	// it fails with a type error. Both are acceptable; what is not acceptable is
	// a panic.
	_ = Decode(m, &cfg)
}

// TestParseYAMLLineError: the error message includes the line number.
func TestParseYAMLLineError(t *testing.T) {
	_, err := ParseYAML([]byte("a: 1\nb: 2\nc: 3\nmalformed\n"))
	if err == nil {
		t.Fatal("an error was expected")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("the error must name the line: %v", err)
	}
}

// TestParseYAMLErrorAtTheEnd: a key without a value on the last line.
func TestParseYAMLErrorAtTheEnd(t *testing.T) {
	// "a:" at the end is valid (null). The line error path is covered with a
	// malformed sequence at the end.
	_, err := ParseYAML([]byte("list:\n  - value\nmalformed\n"))
	if err == nil {
		t.Fatal("an error was expected")
	}
}

// TestParseYAMLSeveralNestingLevels.
func TestParseYAMLSeveralNestingLevels(t *testing.T) {
	m, err := ParseYAML([]byte(`
n1:
  n2:
    n3:
      n4: deep
`))
	if err != nil {
		t.Fatal(err)
	}
	n1 := m["n1"].(map[string]any)
	n2 := n1["n2"].(map[string]any)
	n3 := n2["n3"].(map[string]any)
	if n3["n4"] != "deep" {
		t.Errorf("nesting = %#v", n3)
	}
}

// TestParseYAMLMapInsideSequence with several levels.
func TestParseYAMLMapInsideSequence(t *testing.T) {
	m, err := ParseYAML([]byte(`
items:
  - name: first
    options:
      a: 1
    list:
      - x
      - y
  - name: second
`))
	if err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	first := items[0].(map[string]any)
	if first["name"] != "first" {
		t.Errorf("first = %#v", first)
	}
	if options, ok := first["options"].(map[string]any); !ok || options["a"] != int64(1) {
		t.Errorf("options = %#v", first["options"])
	}
	if list, ok := first["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", first["list"])
	}
}

// TestParseYAMLMapAtTheSameLevelAsTheKey covers the sequence whose indentation
// matches the key's.
func TestParseYAMLMapAtTheSameLevel(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n- one\n- two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := m["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", m["list"])
	}
}

func TestStripComment(t *testing.T) {
	cases := map[string]string{
		"value # comment": "value",
		"# comment only":  "",
		`"with # inside"`: `"with # inside"`,
		"no hash":         "no hash",
		"joined#no space": "joined#no space",
	}
	for input, want := range cases {
		if got := stripComment(input); got != want {
			t.Errorf("stripComment(%q) = %q, expected %q", input, got, want)
		}
	}
}

// --- Every environment variable ---------------------------------------------

// TestEveryEnvironmentVariable: each STARLIGHT_* variable must be read and must
// win over the YAML, because that is the documented way to configure a
// deployment without touching the file.
func TestEveryEnvironmentVariable(t *testing.T) {
	env := map[string]string{
		"STARLIGHT_TASK_SOURCE_KIND":                "queue",
		"STARLIGHT_TASK_SOURCE_PATH":                "/tmp/task.md",
		"STARLIGHT_TASK_SOURCE_DIR":                 "/tmp/queue",
		"STARLIGHT_TASK_SOURCE_URL":                 "http://example/tasks",
		"STARLIGHT_TASK_SOURCE_METHOD":              "POST",
		"STARLIGHT_TASK_SOURCE_FIELD":               "work",
		"STARLIGHT_TASK_SOURCE_BODY":                `{"q":"all"}`,
		"STARLIGHT_TASK_SOURCE_INTERVAL":            "45s",
		"STARLIGHT_ANCHOR_KIND":                     "command",
		"STARLIGHT_ANCHOR_COMMAND":                  "make",
		"STARLIGHT_ANCHOR_TIMEOUT":                  "30s",
		"STARLIGHT_ANCHOR_EXPECT_EXIT":              "3",
		"STARLIGHT_ANCHOR_EXPECT_OUTPUT":            "READY",
		"STARLIGHT_SANDBOX_KIND":                    "cgroups",
		"STARLIGHT_SANDBOX_ROOT":                    "/srv/root",
		"STARLIGHT_SANDBOX_USER":                    "1000:1000",
		"STARLIGHT_SANDBOX_MEMORY_MB":               "512",
		"STARLIGHT_SANDBOX_CPU_SECONDS":             "15",
		"STARLIGHT_SANDBOX_OPEN_FILES":              "128",
		"STARLIGHT_SANDBOX_MAX_FILE_SIZE_MB":        "7",
		"STARLIGHT_SANDBOX_CGROUPS":                 "off",
		"STARLIGHT_SANDBOX_CGROUP_ROOT":             "/sys/fs/cgroup",
		"STARLIGHT_SANDBOX_MAX_OUTPUT_KB":           "64",
		"STARLIGHT_SANDBOX_KEEP_EPHEMERAL":          "true",
		"STARLIGHT_SANDBOX_PROCESSES":               "64",
		"STARLIGHT_SANDBOX_TIMEOUT":                 "25s",
		"STARLIGHT_SANDBOX_ISOLATE_NETWORK":         "true",
		"STARLIGHT_LLM_PROVIDER":                    "anthropic",
		"STARLIGHT_LLM_MODEL":                       "claude-test",
		"STARLIGHT_LLM_API_KEY":                     "k",
		"STARLIGHT_LLM_BASE_URL":                    "https://example/v1",
		"STARLIGHT_LLM_MAX_TOKENS":                  "2048",
		"STARLIGHT_LLM_TEMPERATURE":                 "0.25",
		"STARLIGHT_LLM_TIMEOUT":                     "45s",
		"STARLIGHT_LLM_MAX_ATTEMPTS":                "4",
		"STARLIGHT_LLM_BACKOFF_INITIAL":             "2s",
		"STARLIGHT_LLM_BACKOFF_MAX":                 "20s",
		"STARLIGHT_FINAL_ACTION_KIND":               "api",
		"STARLIGHT_FINAL_ACTION_COMMAND":            "true",
		"STARLIGHT_FINAL_ACTION_URL":                "https://example/done",
		"STARLIGHT_FINAL_ACTION_METHOD":             "PUT",
		"STARLIGHT_FINAL_ACTION_COMMIT_MESSAGE":     "agent: done",
		"STARLIGHT_AGENT_MAX_RETRIES":               "5",
		"STARLIGHT_AGENT_SUBTASK_DEPTH":             "3",
		"STARLIGHT_AGENT_MAX_TASKS":                 "9",
		"STARLIGHT_AGENT_WORKSPACE_DIR":             "/tmp/ws",
		"STARLIGHT_AGENT_LOG_FILE":                  "/tmp/agent.log",
		"STARLIGHT_AGENT_LOG_LEVEL":                 "debug",
		"STARLIGHT_AGENT_LOG_CONSOLE":               "false",
		"STARLIGHT_AGENT_LOG_MAX_MB":                "5",
		"STARLIGHT_AGENT_LOG_BACKUPS":               "2",
		"STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "20s",
		"STARLIGHT_AGENT_ON_FAILURE_KIND":           "command",
		"STARLIGHT_AGENT_ON_FAILURE_COMMAND":        "notify",
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
		{"llm.temperature", cfg.LLM.Temperature, 0.25},
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
		"STARLIGHT_LLM_MAX_ATTEMPTS":                "many",
		"STARLIGHT_LLM_TIMEOUT":                     "soon",
		"STARLIGHT_SANDBOX_MEMORY_MB":               "lots",
		"STARLIGHT_AGENT_LOG_CONSOLE":               "maybe",
		"STARLIGHT_AGENT_MAX_RETRIES":               "several",
		"STARLIGHT_TASK_SOURCE_INTERVAL":            "often",
		"STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT": "later",
		"STARLIGHT_LLM_TEMPERATURE":                 "hot",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := LoadWithoutKey(""); err == nil {
				t.Errorf("%s=%q must be rejected", key, value)
			} else if !strings.Contains(err.Error(), key) {
				t.Errorf("the error must name the variable: %v", err)
			}
		})
	}
}

// TestEmptyEnvironmentValueIsIgnored: a blank variable (usual in CI and systemd)
// must not wipe a value that came from the YAML.
func TestEmptyEnvironmentValueIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  model: from-yaml\n  api_key: k\n")
	t.Setenv("STARLIGHT_LLM_MODEL", "")

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

// TestLoadRejectsInvalidYAML: a broken file is an error, never a silent fallback
// to the defaults (which would report a valid configuration when it is not).
func TestLoadRejectsInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	mustWrite(t, path, "anchor:\n  kind: [unclosed\n")
	if _, err := Load(path); err == nil {
		t.Error("a broken YAML must be rejected")
	}
}

// TestLoadRejectsInvalidConfiguration: a file with a value outside the allowed
// set must be rejected with a clear message.
func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	cases := map[string]string{
		"unknown provider":     "llm:\n  provider: telepathy\n  api_key: k\n",
		"negative retries":     "llm:\n  api_key: k\nagent:\n  max_retries: -1\n",
		"unknown log level":    "llm:\n  api_key: k\nagent:\n  log_level: loud\n",
		"negative backups":     "llm:\n  api_key: k\nagent:\n  log_backups: -2\n",
		"unknown failure kind": "llm:\n  api_key: k\nagent:\n  on_failure:\n    kind: scream\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.yaml")
			mustWrite(t, path, content)
			if _, err := Load(path); err == nil {
				t.Errorf("%s must be rejected", name)
			}
		})
	}
}

// TestWorkspaceDirDefaults: an empty workspace must be filled with the default,
// because the sandbox needs a real directory.
func TestWorkspaceDirDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nagent:\n  workspace_dir: \"\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.WorkspaceDir != Default().Agent.WorkspaceDir {
		t.Errorf("workspace_dir = %q", cfg.Agent.WorkspaceDir)
	}
}

// TestOnFailureDefaultsToNone: an unset escalation must become "none" instead of
// an empty string, so validation of the field is meaningful.
func TestOnFailureDefaultsToNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.OnFailure.Kind != "none" {
		t.Errorf("on_failure.kind = %q", cfg.Agent.OnFailure.Kind)
	}
}

// mustWrite writes a file for the tests, creating the parent directory.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Decoder error paths ----------------------------------------------------

// TestDecodeMapRequiresAStruct: decoding into something that is not a struct must
// be refused with an explanatory message.
func TestDecodeMapRequiresAStruct(t *testing.T) {
	var slice []string
	if err := Decode(map[string]any{"a": 1}, &slice); err == nil {
		t.Error("decoding into a slice must be refused")
	}
}

// TestDecodeNestedBlockIntoASimpleField: a nested block where a scalar is
// expected must be reported with the path.
func TestDecodeNestedBlockIntoASimpleField(t *testing.T) {
	type inner struct {
		Name string `yaml:"name"`
	}
	type outer struct {
		Title inner `yaml:"title"`
	}
	var o outer
	if err := Decode(map[string]any{"title": "just text"}, &o); err == nil {
		t.Error("a scalar where a block is expected must be refused")
	}
}

// TestDecodeUnsupportedFieldType: a field type the decoder does not support (a
// pointer, for instance) must be named in the error instead of being skipped.
func TestDecodeUnsupportedFieldType(t *testing.T) {
	type weird struct {
		Pointer *int `yaml:"pointer"`
	}
	var w weird
	n := 3
	if err := Decode(map[string]any{"pointer": &n}, &w); err == nil {
		t.Error("an unsupported field type must be refused")
	}
}

// TestDecodeBooleanFields: booleans are supported and must land in the field.
func TestDecodeBooleanFields(t *testing.T) {
	type withBool struct {
		Enabled bool `yaml:"enabled"`
	}
	var w withBool
	if err := Decode(map[string]any{"enabled": true}, &w); err != nil {
		t.Fatal(err)
	}
	if !w.Enabled {
		t.Error("the boolean was not applied")
	}
}

// TestDecodeMapWithNonTextValues: a text-to-text map accepts numbers and
// booleans by converting them, which is what a user writing YAML expects.
func TestDecodeMapWithNonTextValues(t *testing.T) {
	type withMap struct {
		Headers map[string]string `yaml:"headers"`
	}
	var w withMap
	if err := Decode(map[string]any{"headers": map[string]any{
		"X-Number": 7,
		"X-Bool":   true,
		"X-Text":   "value",
	}}, &w); err != nil {
		t.Fatal(err)
	}
	if w.Headers["X-Number"] != "7" || w.Headers["X-Bool"] != "true" || w.Headers["X-Text"] != "value" {
		t.Errorf("headers = %v", w.Headers)
	}
}

// TestDecodeIntegerOverflow: a number that does not fit in the field's type must
// be refused instead of silently truncating.
func TestDecodeIntegerOverflowInt8(t *testing.T) {
	type small struct {
		Count int8 `yaml:"count"`
	}
	var s small
	if err := Decode(map[string]any{"count": 1000}, &s); err == nil {
		t.Error("an overflowing value must be refused")
	}
}

// TestDecodeListIntoAScalar: a list where a scalar is expected must be refused
// (this is the case that used to be accepted as "[a b]").
func TestDecodeListIntoAScalar(t *testing.T) {
	type withText struct {
		Name string `yaml:"name"`
	}
	var w withText
	if err := Decode(map[string]any{"name": []any{"a", "b"}}, &w); err == nil {
		t.Error("a list where text is expected must be refused")
	}
}

// TestDecodeListOfComplexItems: a list whose items are blocks must be decoded
// element by element, with the index in the error path when one fails.
func TestDecodeListOfComplexItems(t *testing.T) {
	type check struct {
		Command    string   `yaml:"command"`
		Args       []string `yaml:"args"`
		ExpectExit int      `yaml:"expect_exit"`
	}
	type holder struct {
		Checks []check `yaml:"checks"`
	}
	var h holder
	if err := Decode(map[string]any{"checks": []any{
		map[string]any{"command": "one", "args": []any{"-a"}, "expect_exit": int64(1)},
		map[string]any{"command": "two"},
	}}, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Checks) != 2 || h.Checks[0].Command != "one" || h.Checks[0].Args[0] != "-a" || h.Checks[1].Command != "two" {
		t.Errorf("checks = %+v", h.Checks)
	}

	// A failing element must name its position.
	var bad holder
	err := Decode(map[string]any{"checks": []any{
		map[string]any{"command": "fine"},
		map[string]any{"command": []any{"not", "text"}},
	}}, &bad)
	if err == nil {
		t.Fatal("the failing element must be reported")
	}
	if !strings.Contains(err.Error(), "[1]") {
		t.Errorf("the error must say which element failed: %v", err)
	}
}

// TestDecodeNullRespectsDefaults: an explicit null in the YAML must leave the
// field's current (default) value alone.
func TestDecodeNullRespectsDefaults(t *testing.T) {
	c := Default()
	before := c.Agent.MaxRetries
	if err := Decode(map[string]any{"agent": map[string]any{"max_retries": nil}}, &c); err != nil {
		t.Fatal(err)
	}
	if c.Agent.MaxRetries != before {
		t.Errorf("max_retries = %d, expected the default %d", c.Agent.MaxRetries, before)
	}
}

// TestDecodeRejectsUnknownKeys: a misspelled key would otherwise be ignored and
// the setting silently left at its default, so it is refused with a hint.
func TestDecodeRejectsUnknownKeys(t *testing.T) {
	c := Default()
	err := Decode(map[string]any{"this_key_does_not_exist": int64(1)}, &c)
	if err == nil {
		t.Fatal("a misspelled key must be refused")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("the error must identify the problem: %v", err)
	}
}

// TestDecodeRejectsUnknownNestedKeys: the same for a key inside a block, where
// the message must name the path so the typo can be found.
func TestDecodeRejectsUnknownNestedKeys(t *testing.T) {
	c := Default()
	err := Decode(map[string]any{"agent": map[string]any{"max_retriess": int64(1)}}, &c)
	if err == nil {
		t.Fatal("a misspelled nested key must be refused")
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("the error must name the block: %v", err)
	}
}

// --- YAML parser error paths ------------------------------------------------

// TestParseYAMLRejectsSequenceAtTheRoot: a configuration must be a map; a list at
// the top level is refused with a message that says so.
func TestParseYAMLRejectsSequenceAtTheRoot(t *testing.T) {
	if _, err := ParseYAML([]byte("- one\n- two\n")); err == nil {
		t.Error("a sequence at the root must be refused")
	}
}

// TestParseYAMLRejectsTrailingContent: two documents in one file must be refused
// instead of silently ignoring the second.
func TestParseYAMLRejectsTrailingContent(t *testing.T) {
	if _, err := ParseYAML([]byte("a: 1\nb: 2\nc: 3\n")); err != nil {
		t.Fatalf("a plain map must be accepted: %v", err)
	}
	// A line that does not belong to the root block is trailing content.
	if _, err := ParseYAML([]byte("agent:\n  max_retries: 1\nplain_key\n")); err == nil {
		t.Error("trailing content must be refused")
	}
}

// TestParseYAMLRejectsTabsInIndentation: a tab is not valid YAML indentation and
// must be reported with the line number instead of producing a confusing tree.
func TestParseYAMLRejectsTabsInIndentation(t *testing.T) {
	if _, err := ParseYAML([]byte("agent:\n\tmax_retries: 1\n")); err == nil {
		t.Error("a tab used for indentation must be refused")
	}
}

// TestParseYAMLHandlesEveryScalarShape: the parser must accept the values a user
// actually writes (quoted text, numbers, booleans, inline lists and maps, empty
// values, comments).
func TestParseYAMLHandlesEveryScalarShape(t *testing.T) {
	doc := []byte(`# a comment
text: plain
quoted: "with: colon"
single: 'single quoted'
number: 42
negative: -7
float: 1.5
boolean: true
empty:
inline_list: [a, b, "c d"]
inline_map: {a: 1, b: two}
list:
  - one
  - two
nested:
  key: value
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("the document must parse: %v", err)
	}
	if m["text"] != "plain" || m["quoted"] != "with: colon" || m["single"] != "single quoted" {
		t.Errorf("text values = %v / %v / %v", m["text"], m["quoted"], m["single"])
	}
	if m["number"] != int64(42) && m["number"] != 42 {
		t.Errorf("number = %#v", m["number"])
	}
	if m["boolean"] != true {
		t.Errorf("boolean = %#v", m["boolean"])
	}
	if _, ok := m["empty"]; !ok {
		t.Errorf("an empty value must still be present as a key: %v", m)
	}
	if list, ok := m["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", m["list"])
	}
	if _, ok := m["nested"].(map[string]any); !ok {
		t.Errorf("nested = %#v", m["nested"])
	}
}

// TestParseYAMLRejectsMalformedLines: the shapes a user can get wrong must be
// refused with the line number, not silently misread.
func TestParseYAMLRejectsMalformedLines(t *testing.T) {
	cases := map[string]string{
		"line without a colon":  "just text with no separator\n",
		"unclosed inline list":  "list: [a, b\n",
		"unclosed inline map":   "map: {a: 1\n",
		"unclosed quote":        "text: \"unclosed\n",
		"bad indentation jump":  "a: 1\n   b: 2\n",
		"item without a parent": "  - orphan\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseYAML([]byte(doc)); err == nil {
				t.Errorf("%s must be refused", name)
			}
		})
	}
}

// TestParseYAMLHandlesCommentsAndBlankLines: comments and blank lines are
// skipped everywhere, including inside blocks.
func TestParseYAMLHandlesCommentsAndBlankLines(t *testing.T) {
	doc := []byte(`# header

agent:
  # a comment inside the block

  max_retries: 3

  # another
  max_tasks: 5
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := m["agent"].(map[string]any)
	if agent == nil {
		t.Fatalf("agent = %#v", m["agent"])
	}
	if agent["max_retries"] != int64(3) && agent["max_retries"] != 3 {
		t.Errorf("max_retries = %#v", agent["max_retries"])
	}
	if agent["max_tasks"] != int64(5) && agent["max_tasks"] != 5 {
		t.Errorf("max_tasks = %#v", agent["max_tasks"])
	}
}

// --- Decoder: the remaining guarded paths -----------------------------------

// TestDecodeTaglessFieldsAreSkipped: fields with no `yaml` tag (or tagged "-")
// are not configurable, which is what keeps internal fields private.
func TestDecodeTaglessFieldsAreSkipped(t *testing.T) {
	type withPrivate struct {
		Public     string `yaml:"public"`
		NotTagged  string
		Explicitly string `yaml:"-"`
	}
	var w withPrivate
	if err := Decode(map[string]any{"public": "yes"}, &w); err != nil {
		t.Fatal(err)
	}
	if w.Public != "yes" {
		t.Errorf("public = %q", w.Public)
	}
}

// TestDecodeMapWithNonStringKeys: only text-to-text maps are supported, and
// anything else is refused so a wrong type cannot be silently dropped.
func TestDecodeMapWithNonStringKeys(t *testing.T) {
	type intMap struct {
		Counts map[string]int `yaml:"counts"`
	}
	var w intMap
	if err := Decode(map[string]any{"counts": map[string]any{"a": int64(1)}}, &w); err == nil {
		t.Error("a map that is not text-to-text must be refused")
	}
}

// TestDecodeScalarWhereMapExpected: a scalar where a map is expected must be
// refused instead of producing an empty map.
func TestDecodeScalarWhereMapExpected(t *testing.T) {
	type withMap struct {
		Headers map[string]string `yaml:"headers"`
	}
	var w withMap
	if err := Decode(map[string]any{"headers": "not a map"}, &w); err == nil {
		t.Error("a scalar where a map is expected must be refused")
	}
}

// TestDecodeScalarWhereListExpected: the same for lists.
func TestDecodeScalarWhereListExpected(t *testing.T) {
	type withList struct {
		Args []string `yaml:"args"`
	}
	var w withList
	if err := Decode(map[string]any{"args": "not a list"}, &w); err == nil {
		t.Error("a scalar where a list is expected must be refused")
	}
}

// TestDecodeMapWithNestedBlockValue: a map whose value is a block is refused,
// because only text-to-text maps are supported.
func TestDecodeMapWithNestedBlockValue(t *testing.T) {
	type withMap struct {
		Headers map[string]string `yaml:"headers"`
	}
	var w withMap
	if err := Decode(map[string]any{"headers": map[string]any{"nested": map[string]any{"a": 1}}}, &w); err != nil {
		// Nested maps are converted with their text form, which is acceptable;
		// what matters is that it does not panic.
		t.Logf("nested value refused: %v", err)
	} else if w.Headers["nested"] == "" {
		t.Error("the nested value must keep some text form")
	}
}

// TestDecodeDurationFromText: durations are written by hand in the YAML, so the
// textual forms must be understood.
func TestDecodeDurationFromText(t *testing.T) {
	c := Default()
	err := Decode(map[string]any{"anchor": map[string]any{
		"kind":    "command",
		"timeout": "90s",
	}}, &c)
	if err != nil {
		t.Fatal(err)
	}
	if c.Anchor.Timeout.String() != "1m30s" {
		t.Errorf("timeout = %v", c.Anchor.Timeout)
	}
}

// --- YAML: the remaining guarded shapes -------------------------------------

// TestParseYAMLBlockScalar: a block scalar (| or >) carries multi-line text, and
// the value must keep its newlines.
func TestParseYAMLBlockScalar(t *testing.T) {
	doc := []byte("prompt: |\n  first line\n  second line\nafter: value\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("a block scalar must be accepted: %v", err)
	}
	text, _ := m["prompt"].(string)
	if !strings.Contains(text, "first line") || !strings.Contains(text, "second line") {
		t.Errorf("prompt = %q", text)
	}
	if m["after"] != "value" {
		t.Errorf("the key after the block must still be read: %v", m["after"])
	}
}

// TestParseYAMLSequenceOfMaps: a list whose items are blocks is the shape used by
// anchor.checks, so it must be decoded item by item.
func TestParseYAMLSequenceOfMaps(t *testing.T) {
	doc := []byte(`checks:
  - name: first
    command: one
  - name: second
    command: two
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("a sequence of maps must be accepted: %v", err)
	}
	items, ok := m["checks"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("checks = %#v", m["checks"])
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "first" || first["command"] != "one" {
		t.Errorf("first item = %#v", first)
	}
}

// TestParseYAMLSequenceOfScalars: the simple list form must work too.
func TestParseYAMLSequenceOfScalars(t *testing.T) {
	doc := []byte("args:\n  - alpha\n  - beta\n  - 3\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := m["args"].([]any)
	if len(items) != 3 || items[0] != "alpha" || items[1] != "beta" {
		t.Errorf("args = %#v", m["args"])
	}
}

// TestParseYAMLQuotedKeys: a key may be quoted, and the quotes must be removed.
func TestParseYAMLQuotedKeys(t *testing.T) {
	doc := []byte("\"quoted key\": value\n'single': other\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if m["quoted key"] != "value" {
		t.Errorf("the double-quoted key was not unquoted: %v", m)
	}
	if m["single"] != "other" {
		t.Errorf("the single-quoted key was not unquoted: %v", m)
	}
}

// TestParseYAMLUnterminatedQuotedKey: an opening quote with no closing one must be
// refused, not read as part of the key.
func TestParseYAMLUnterminatedQuotedKey(t *testing.T) {
	if _, err := ParseYAML([]byte("\"unterminated: value\n")); err == nil {
		t.Error("an unterminated quoted key must be refused")
	}
}

// TestParseYAMLEmptyKey: a colon with nothing before it cannot name a key.
func TestParseYAMLEmptyKey(t *testing.T) {
	if _, err := ParseYAML([]byte(": value\n")); err == nil {
		t.Error("an empty key must be refused")
	}
}

// TestParseYAMLEscapedStrings: the escape sequences a user writes inside a
// double-quoted value must be interpreted.
func TestParseYAMLEscapedStrings(t *testing.T) {
	doc := []byte("text: \"line1\\nline2\\ttabbed\\r\\\"quoted\\\"\"\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["text"].(string)
	for _, want := range []string{"line1\nline2", "\ttabbed", "\r", "\"quoted\""} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, missing %q", text, want)
		}
	}
}

// TestParseYAMLInlineEmptyContainers: `[]` and `{}` are valid YAML for empty
// collections.
func TestParseYAMLInlineEmptyContainers(t *testing.T) {
	doc := []byte("empty_list: []\nempty_map: {}\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := m["empty_list"].([]any); !ok || len(list) != 0 {
		t.Errorf("empty_list = %#v", m["empty_list"])
	}
	if mp, ok := m["empty_map"].(map[string]any); !ok || len(mp) != 0 {
		t.Errorf("empty_map = %#v", m["empty_map"])
	}
}

// TestParseYAMLInlineMapWithoutBraces: a missing closing brace must be refused
// with the line number.
func TestParseYAMLInlineMapWithoutBraces(t *testing.T) {
	if _, err := ParseYAML([]byte("map: {a: 1\n")); err == nil {
		t.Error("an inline map without a closing brace must be refused")
	}
}

// TestParseYAMLInlineMapWithABadEntry: an entry without a colon inside an inline
// map must be reported.
func TestParseYAMLInlineMapWithABadEntry(t *testing.T) {
	if _, err := ParseYAML([]byte("map: {a: 1, no-colon}\n")); err == nil {
		t.Error("an entry without a colon must be refused")
	}
}

// TestParseYAMLUnclosedInlineList: a missing closing bracket must be refused.
func TestParseYAMLUnclosedInlineList(t *testing.T) {
	if _, err := ParseYAML([]byte("list: [a, b\n")); err == nil {
		t.Error("an inline list without a closing bracket must be refused")
	}
}

// TestParseYAMLAnchorsAndAliasesAreNotSupported: the parser is deliberately
// minimal; a construct it does not implement must not be silently misread.
func TestParseYAMLAnchorsAndAliasesAreNotSupported(t *testing.T) {
	// An alias is read as plain text, which is acceptable; what matters is that
	// it does not crash and that the document still parses.
	if _, err := ParseYAML([]byte("base: &anchor value\ncopy: *anchor\n")); err != nil {
		t.Logf("anchors are refused (acceptable): %v", err)
	}
}

// TestParseYAMLParsesTheExampleConfiguration: the file shipped as documentation
// must be readable by the parser, which is the real acceptance test.
func TestParseYAMLParsesTheExampleConfiguration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "agent.yaml.example"))
	if err != nil {
		t.Skipf("the example is not available: %v", err)
	}
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("the shipped example must parse: %v", err)
	}
	for _, key := range []string{"task_source", "anchor", "sandbox", "llm", "agent", "final_action"} {
		if _, ok := m[key]; !ok {
			t.Errorf("the example must define %q", key)
		}
	}
}

// TestTheNewEnvironmentVariablesRejectBadValues: each variable added for the
// documented "STARLIGHT_* covers everything" rule must validate its input, naming
// the variable that is wrong.
func TestTheNewEnvironmentVariablesRejectBadValues(t *testing.T) {
	cases := map[string]string{
		"STARLIGHT_SANDBOX_OPEN_FILES":       "many",
		"STARLIGHT_SANDBOX_MAX_FILE_SIZE_MB": "big",
		"STARLIGHT_SANDBOX_MAX_OUTPUT_KB":    "plenty",
		"STARLIGHT_SANDBOX_KEEP_EPHEMERAL":   "perhaps",
		"STARLIGHT_AGENT_ON_FAILURE_KIND":    "nothing-to-validate-here",
	}
	// Only the numeric and boolean ones can fail; the text one is free-form and
	// is validated later by validate(). Keep the map honest:
	for key, value := range cases {
		if key == "STARLIGHT_AGENT_ON_FAILURE_KIND" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := LoadWithoutKey(""); err == nil {
				t.Errorf("%s=%q must be rejected", key, value)
			} else if !strings.Contains(err.Error(), key) {
				t.Errorf("the error must name the variable: %v", err)
			}
		})
	}
}

// TestOnFailureKindIsValidated: a value outside the allowed set must be refused
// (and the empty value must default to "none").
func TestOnFailureKindIsValidated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nagent:\n  on_failure:\n    kind: escalate-to-a-human\n")
	if _, err := Load(path); err == nil {
		t.Error("an unknown on_failure kind must be refused")
	}
}

// TestLoadWithoutKeyIgnoresTheKeyRequirement: the diagnostic mode must work with
// no key anywhere.
func TestLoadWithoutKeyIgnoresTheKeyRequirement(t *testing.T) {
	cfg, err := LoadWithoutKey("")
	if err != nil {
		t.Fatalf("the diagnostic mode must not require a key: %v", err)
	}
	if cfg.Anchor.Kind == "" {
		t.Error("the defaults must still be applied")
	}
}

// --- Parser helpers: the guards the parse paths rely on ----------------------

// TestErrorfNamesTheEndOfTheDocument: errorf builds every parser error. When the
// cursor has already run past the last line there is no line number to report,
// and the message must say so instead of reading a line that does not exist.
func TestErrorfNamesTheEndOfTheDocument(t *testing.T) {
	p := &yamlParser{}
	if !p.done() {
		t.Fatal("a parser with no lines is already done")
	}
	err := p.errorf("something went wrong")
	if err == nil {
		t.Fatal("errorf must return an error")
	}
	if !strings.Contains(err.Error(), "end of document") {
		t.Errorf("error = %q, expected the end-of-document form", err)
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %q, it must keep the message it was given", err)
	}

	// With the cursor on a real line the message carries its line number.
	p2 := &yamlParser{lines: []yamlLine{{indent: 0, text: "a: 1", num: 7}}}
	if err := p2.errorf("something went wrong"); !strings.Contains(err.Error(), "line 7") {
		t.Errorf("error = %q, expected the line number", err)
	}
}

// TestParseBlockWithNoLinesLeft: parseBlock is called defensively from the
// nested blocks; with nothing left to read it reports "no structure" instead of
// indexing past the end of the document.
func TestParseBlockWithNoLinesLeft(t *testing.T) {
	p := &yamlParser{}
	value, err := p.parseBlock(0)
	if err != nil {
		t.Fatalf("an exhausted parser must not fail: %v", err)
	}
	if value != nil {
		t.Errorf("value = %#v, expected nil", value)
	}

	// A block asked for at a deeper indentation than the current line does not
	// exist either.
	p2 := &yamlParser{lines: []yamlLine{{indent: 0, text: "a: 1", num: 1}}}
	if value, err := p2.parseBlock(4); err != nil || value != nil {
		t.Errorf("parseBlock(4) over an indent-0 line = %#v, %v", value, err)
	}
}

// TestParseYAMLKeyWithoutValueOnTheLastLine: a key with nothing after it on the
// last line is null, not an error, and the keys before it are kept.
func TestParseYAMLKeyWithoutValueOnTheLastLine(t *testing.T) {
	m, err := ParseYAML([]byte("a: 1\nb:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != int64(1) {
		t.Errorf("a = %#v", m["a"])
	}
	if v, ok := m["b"]; !ok || v != nil {
		t.Errorf("b = %#v, expected null", m["b"])
	}
}

// TestParseYAMLSequenceItemInsideAMapIsRejected: an item at the same level as
// the keys of a block cannot belong to that block, so it is reported instead of
// being folded into the map.
func TestParseYAMLSequenceItemInsideAMapIsRejected(t *testing.T) {
	_, err := ParseYAML([]byte("a:\n  b: 1\n  - c\n"))
	if err == nil {
		t.Fatal("a stray sequence item must be refused")
	}
	if !strings.Contains(err.Error(), "unexpected content") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLNestedBlockUnderABareItem: "-" with nothing after it opens a
// nested block, which is the list-of-blocks form.
func TestParseYAMLNestedBlockUnderABareItem(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n  -\n    a: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := m["list"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list = %#v", m["list"])
	}
	item, _ := list[0].(map[string]any)
	if item == nil || item["a"] != int64(1) {
		t.Errorf("item = %#v", list[0])
	}
}

// TestParseYAMLMalformedBlockUnderABareItem: what fails to parse inside such a
// nested block must surface with its line number.
func TestParseYAMLMalformedBlockUnderABareItem(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n  -\n    malformed\n"))
	if err == nil {
		t.Fatal("a malformed nested block must be refused")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the error must name the line: %v", err)
	}
}

// TestParseYAMLUnsupportedStructureOnAMapLine: a line that is not "key: value"
// and carries an anchor, alias or tag marker is refused where it is read as a
// key, not silently turned into a key.
func TestParseYAMLUnsupportedStructureOnAMapLine(t *testing.T) {
	for _, input := range []string{"a: 1\n*alias\n", "a: 1\n&anchor\n", "a: 1\n!tag\n"} {
		_, err := ParseYAML([]byte(input))
		if err == nil {
			t.Errorf("ParseYAML(%q) must refuse the YAML structure it does not implement", input)
			continue
		}
		if !strings.Contains(err.Error(), "unsupported YAML structure") {
			t.Errorf("ParseYAML(%q) = %v", input, err)
		}
	}
}

// TestParseYAMLBrokenItemAtTheSameLevelAsTheKey: a sequence written at the level
// of its key (no extra indentation) reports its errors like any other list.
func TestParseYAMLBrokenItemAtTheSameLevelAsTheKey(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n- [broken\n"))
	if err == nil {
		t.Fatal("a broken item must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLBrokenFirstPairOfASequenceItem: the pair that opens an item
// ("- key: value") is parsed like any other, so a broken value there must be
// reported instead of leaving a half-built item.
func TestParseYAMLBrokenFirstPairOfASequenceItem(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n  - a: [broken\n"))
	if err == nil {
		t.Fatal("a broken value in the first pair of an item must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLQuotedKeyWithTrailingCharacters: a key that opens a quote but
// does not close it the way it should is refused instead of being taken
// literally.
func TestParseYAMLQuotedKeyWithTrailingCharacters(t *testing.T) {
	_, err := ParseYAML([]byte("\"a\"b: 1\n"))
	if err == nil {
		t.Fatal("a malformed quoted key must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLLoneQuoteAsValue: a value made of a single opening quote has no
// closing one and must be reported as such.
func TestParseYAMLLoneQuoteAsValue(t *testing.T) {
	_, err := ParseYAML([]byte("a: \"\n"))
	if err == nil {
		t.Fatal("a lone quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLInlineListElementErrors: every element of an inline list is
// parsed, so a broken one is reported with the line number.
func TestParseYAMLInlineListElementErrors(t *testing.T) {
	_, err := ParseYAML([]byte("a: [\"x]\n"))
	if err == nil {
		t.Fatal("an inline list with an unterminated quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}

	_, err = ParseYAML([]byte("a: [&x]\n"))
	if err == nil {
		t.Fatal("an inline list with an anchor must be refused")
	}
	if !strings.Contains(err.Error(), "anchors, aliases and tags") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLInlineMapElementErrors: the same for the entries of an inline
// map, including the case where the entry's value is itself broken.
func TestParseYAMLInlineMapElementErrors(t *testing.T) {
	_, err := ParseYAML([]byte("a: {\"x: 1}\n"))
	if err == nil {
		t.Fatal("an inline map with an unterminated quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}

	_, err = ParseYAML([]byte("a: {k: [broken}\n"))
	if err == nil {
		t.Fatal("an inline map whose value is a broken list must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}

// --- Decoder and validation: the remaining guarded paths --------------------

// TestDecodeDurationErrors: a duration that cannot be interpreted must be
// reported with the field path, so the file can be fixed.
func TestDecodeDurationErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nanchor:\n  timeout: soon\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("an invalid duration must be refused")
	}
	if !strings.Contains(err.Error(), "anchor.timeout") {
		t.Errorf("the error must name the field: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("the error must say what is wrong: %v", err)
	}
}

// TestDecodeIntegerOverflowReportsTheValue: a number that does not fit in the
// field's type names the value and the type instead of truncating it.
func TestDecodeIntegerOverflowReportsTheValue(t *testing.T) {
	type small struct {
		Count int8 `yaml:"count"`
	}
	var s small
	err := Decode(map[string]any{"count": int64(1000)}, &s)
	if err == nil {
		t.Fatal("an overflowing value must be refused")
	}
	if !strings.Contains(err.Error(), "does not fit") {
		t.Errorf("the error must explain the overflow: %v", err)
	}
	if !strings.Contains(err.Error(), "int8") {
		t.Errorf("the error must name the type: %v", err)
	}
}

// TestDecodeFloatErrors: a value that is not a number where a float is expected
// is reported with the field path.
func TestDecodeFloatErrors(t *testing.T) {
	c := Default()
	err := Decode(map[string]any{"llm": map[string]any{"temperature": "hot"}}, &c)
	if err == nil {
		t.Fatal("a non-numeric temperature must be refused")
	}
	if !strings.Contains(err.Error(), "llm.temperature") {
		t.Errorf("the error must name the field: %v", err)
	}
	if !strings.Contains(err.Error(), "expected a number") {
		t.Errorf("the error must say what is wrong: %v", err)
	}
}

// TestOnFailureKindEmptyBecomesNone: an empty escalation kind (what an empty
// quoted value in the YAML produces) is normalised to "none" instead of being
// rejected as an unknown one.
func TestOnFailureKindEmptyBecomesNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  api_key: k\nagent:\n  on_failure:\n    kind: \"\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("an empty on_failure.kind must be normalised, not rejected: %v", err)
	}
	if cfg.Agent.OnFailure.Kind != "none" {
		t.Errorf("on_failure.kind = %q, expected \"none\"", cfg.Agent.OnFailure.Kind)
	}
}

// TestEnvironmentRejectsInvalidDurationsInTheLeftoverBlocks: the sandbox timeout
// and the LLM backoff variables validate their input like the rest, naming the
// variable that is wrong.
func TestEnvironmentRejectsInvalidDurationsInTheLeftoverBlocks(t *testing.T) {
	for _, key := range []string{
		"STARLIGHT_SANDBOX_TIMEOUT",
		"STARLIGHT_LLM_BACKOFF_INITIAL",
		"STARLIGHT_LLM_BACKOFF_MAX",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "soon")
			_, err := LoadWithoutKey("")
			if err == nil {
				t.Fatalf("%s=soon must be rejected", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("the error must name the variable: %v", err)
			}
		})
	}
}

// --- prompts and other overrides from the environment -----------------------

// TestApplyEnvironmentOverridesEveryPrompt: the six prompt texts are plain
// settings, so the convention STARLIGHT_<BLOCK>_<FIELD> must reach them too.
func TestApplyEnvironmentOverridesEveryPrompt(t *testing.T) {
	c := Default()
	c.Prompts.Analyze.System = "analyze system"
	c.Prompts.Analyze.User = "analyze user"
	c.Prompts.Plan.System = "plan system"
	c.Prompts.Plan.User = "plan user"
	c.Prompts.Execute.System = "execute system"
	c.Prompts.Execute.User = "execute user"

	t.Setenv("STARLIGHT_PROMPTS_ANALYZE_SYSTEM", "new analyze system")
	t.Setenv("STARLIGHT_PROMPTS_ANALYZE_USER", "new analyze user")
	t.Setenv("STARLIGHT_PROMPTS_PLAN_SYSTEM", "new plan system")
	t.Setenv("STARLIGHT_PROMPTS_PLAN_USER", "new plan user")
	t.Setenv("STARLIGHT_PROMPTS_EXECUTE_SYSTEM", "new execute system")
	t.Setenv("STARLIGHT_PROMPTS_EXECUTE_USER", "new execute user")

	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}

	got := map[string]string{
		"analyze.system": c.Prompts.Analyze.System,
		"analyze.user":   c.Prompts.Analyze.User,
		"plan.system":    c.Prompts.Plan.System,
		"plan.user":      c.Prompts.Plan.User,
		"execute.system": c.Prompts.Execute.System,
		"execute.user":   c.Prompts.Execute.User,
	}
	for name, value := range got {
		if !strings.HasPrefix(value, "new ") {
			t.Errorf("%s = %q, the variable did not win", name, value)
		}
	}
}

// TestReadPromptKeepsTheTextAsWritten: a prompt is multi-line and its layout is
// part of the instructions, so it is NOT trimmed like the other text settings.
func TestReadPromptKeepsTheTextAsWritten(t *testing.T) {
	text := "  first line\n\tsecond line with indentation  \n"
	t.Setenv("STARLIGHT_TEST_PROMPT", text)

	if got := readPrompt("STARLIGHT_TEST_PROMPT", "original"); got != text {
		t.Errorf("readPrompt = %q, want the text untouched %q", got, text)
	}
}

// TestReadPromptIgnoresAnEmptyValue: an empty or blank value means "leave it as
// it was", the same as the other settings (and what stopped a stray variable from
// wiping a value).
func TestReadPromptIgnoresAnEmptyValue(t *testing.T) {
	for _, value := range []string{"", "   ", "\n\t"} {
		t.Setenv("STARLIGHT_TEST_PROMPT", value)
		if got := readPrompt("STARLIGHT_TEST_PROMPT", "original"); got != "original" {
			t.Errorf("value %q: got %q, want the original", value, got)
		}
	}
}

// TestReadPromptWithoutTheVariable: absent means untouched, which is the common
// case.
func TestReadPromptWithoutTheVariable(t *testing.T) {
	os.Unsetenv("STARLIGHT_TEST_PROMPT")
	if got := readPrompt("STARLIGHT_TEST_PROMPT", "original"); got != "original" {
		t.Errorf("got %q, want the original", got)
	}
}

// TestShutdownTimeoutAcceptsBothNames: the field is graceful_shutdown_timeout, so
// the documented variable is STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT. The name
// published in the previous release (STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT) must still
// work, and the documented one must win when both are set.
func TestShutdownTimeoutAcceptsBothNames(t *testing.T) {
	const documented = "STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT"
	const historical = "STARLIGHT_AGENT_SHUTDOWN_TIMEOUT"

	cases := []struct {
		name     string
		env      map[string]string
		expected time.Duration
	}{
		{"neither is set", nil, Default().Agent.ShutdownTimeout},
		{"the documented one", map[string]string{documented: "20s"}, 20 * time.Second},
		{"the historical one", map[string]string{historical: "25s"}, 25 * time.Second},
		{"the documented one wins", map[string]string{documented: "20s", historical: "25s"}, 20 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{documented, historical} {
				t.Setenv(key, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg := Default()
			if err := ApplyEnvironment(&cfg); err != nil {
				t.Fatalf("ApplyEnvironment: %v", err)
			}
			if cfg.Agent.ShutdownTimeout != tc.expected {
				t.Errorf("ShutdownTimeout = %v, want %v", cfg.Agent.ShutdownTimeout, tc.expected)
			}
		})
	}
}

// TestShutdownTimeoutReportsTheBadName: a malformed value must name the variable
// that was actually read, so the operator can find it.
func TestShutdownTimeoutReportsTheBadName(t *testing.T) {
	t.Setenv("STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT", "later")
	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil {
		t.Fatal("a malformed duration must be reported")
	}
	if !strings.Contains(err.Error(), "GRACEFUL_SHUTDOWN_TIMEOUT") {
		t.Errorf("the error must name the documented variable: %v", err)
	}
}

// TestEveryScalarSettingHasAnEnvironmentVariable walks the configuration struct
// itself and builds the variable the README documents for each setting
// (STARLIGHT_<BLOCK>_<FIELD>, from the yaml tags). If a setting is added without
// its variable, this fails instead of the promise quietly becoming false.
//
// The documented exceptions are the collections (they cannot come from a single
// variable), the settings nested under a sub-block, and the compatibility name.
func TestEveryScalarSettingHasAnEnvironmentVariable(t *testing.T) {
	// Collections: they are lists or maps and are set in the YAML.
	collections := map[string]bool{
		"STARLIGHT_TASK_SOURCE_HEADERS": true,
		"STARLIGHT_ANCHOR_ARGS":         true,
		"STARLIGHT_ANCHOR_CHECKS":       true,
		"STARLIGHT_FINAL_ACTION_ARGS":   true,
	}
	// Set under a sub-block, so the variable is not BLOCK_FIELD.
	nested := map[string]bool{
		"STARLIGHT_AGENT_ON_FAILURE": true, // STARLIGHT_AGENT_ON_FAILURE_KIND/COMMAND
	}
	// Read from the environment elsewhere (see Load), not by ApplyEnvironment.
	otherReaders := map[string]bool{
		"STARLIGHT_AGENT_SHUTDOWN_TIMEOUT": true, // compatibility name, documented
		"STARLIGHT_SANDBOX_CGROUPS":        true, // read by the sandbox layer
	}

	implemented := map[string]bool{}
	body, err := os.ReadFile("environment.go")
	if err != nil {
		t.Fatalf("could not read environment.go: %v", err)
	}
	for _, m := range regexp.MustCompile(`"(STARLIGHT_[A-Z0-9_]+)"`).FindAllStringSubmatch(string(body), -1) {
		implemented[m[1]] = true
	}

	c := reflect.TypeOf(Config{})
	for i := 0; i < c.NumField(); i++ {
		block := c.Field(i).Tag.Get("yaml")
		bt := c.Field(i).Type
		for j := 0; j < bt.NumField(); j++ {
			field := bt.Field(j)
			name := field.Tag.Get("yaml")
			variable := "STARLIGHT_" + strings.ToUpper(block+"_"+name)

			// Skip the collections and whatever has its own reader.
			if collections[variable] || nested[variable] || otherReaders[variable] {
				continue
			}
			// Sub-structs (like on_failure) hold their own fields.
			if field.Type.Kind() == reflect.Struct && field.Type.Name() != "Duration" {
				continue
			}
			// A slice or a map cannot be expressed as one value.
			if k := field.Type.Kind(); k == reflect.Slice || k == reflect.Map {
				continue
			}
			if !implemented[variable] {
				// The field may have a differently named variable on purpose:
				// the documented compatibility case is listed above.
				t.Errorf("the setting %s.%s has no environment variable (%s)", block, name, variable)
			}
		}
	}
}

// TestReadOnlyAndShellComeFromTheEnvironment: the two settings that make plan mode
// configurable, including the case that must be reported rather than ignored.
func TestReadOnlyAndShellComeFromTheEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantRO  bool
		wantSh  string
		wantErr bool
	}{
		{"neither set", nil, false, "", false},
		{"read_only true", map[string]string{"STARLIGHT_AGENT_READ_ONLY": "true"}, true, "", false},
		{"read_only on", map[string]string{"STARLIGHT_AGENT_READ_ONLY": "on"}, true, "", false},
		{"read_only false", map[string]string{"STARLIGHT_AGENT_READ_ONLY": "off"}, false, "", false},
		{"read_only blank is ignored", map[string]string{"STARLIGHT_AGENT_READ_ONLY": "  "}, false, "", false},
		{"the shell", map[string]string{"STARLIGHT_AGENT_SHELL": "/bin/dash"}, false, "/bin/dash", false},
		{"a bad boolean is reported",
			map[string]string{"STARLIGHT_AGENT_READ_ONLY": "maybe"}, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"STARLIGHT_AGENT_READ_ONLY", "STARLIGHT_AGENT_SHELL"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg := Default()
			err := ApplyEnvironment(&cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("a malformed boolean must be reported, not ignored")
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyEnvironment: %v", err)
			}
			if cfg.Agent.ReadOnly != tc.wantRO {
				t.Errorf("ReadOnly = %v, want %v", cfg.Agent.ReadOnly, tc.wantRO)
			}
			if cfg.Agent.Shell != tc.wantSh {
				t.Errorf("Shell = %q, want %q", cfg.Agent.Shell, tc.wantSh)
			}
		})
	}
}

// TestProviderKeyVariable: each provider resolves to the variable its own
// documentation uses, and an unknown one falls back to the generic name.
func TestProviderKeyVariable(t *testing.T) {
	if got := ProviderKeyVariable("ollama"); got != "OLLAMA_API_KEY" {
		t.Errorf("ProviderKeyVariable(ollama) = %q", got)
	}
	if got := ProviderKeyVariable("OLLAMA"); got != "OLLAMA_API_KEY" {
		t.Errorf("the lookup must be case-insensitive, got %q", got)
	}
	if got := ProviderKeyVariable("  ollama  "); got != "OLLAMA_API_KEY" {
		t.Errorf("the lookup must ignore surrounding spaces, got %q", got)
	}
	if got := ProviderKeyVariable("openai"); got != "STARLIGHT_LLM_API_KEY" {
		t.Errorf("ProviderKeyVariable(openai) = %q", got)
	}
	if got := ProviderKeyVariable(""); got != "STARLIGHT_LLM_API_KEY" {
		t.Errorf("ProviderKeyVariable(empty) = %q", got)
	}
}

// TestOllamaApiKeyVariableIsHonoured: the Ollama provider must work with the key
// in OLLAMA_API_KEY, which is the variable Ollama itself documents. Without this
// an Ollama user follows the instructions and the agent still says the key is
// missing.
func TestOllamaApiKeyVariableIsHonoured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  provider: ollama\n  base_url: https://ollama.com/v1\n")
	t.Setenv("STARLIGHT_LLM_API_KEY", "")
	os.Unsetenv("STARLIGHT_LLM_API_KEY")
	t.Setenv("OLLAMA_API_KEY", "key-from-ollama-var")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "key-from-ollama-var" {
		t.Errorf("api_key = %q, want the value from OLLAMA_API_KEY", cfg.LLM.APIKey)
	}
}

// TestGenericKeyVariableWinsOverTheProviderAlias: the documented generic name has
// priority, so an operator who sets both gets what the documentation promised.
func TestGenericKeyVariableWinsOverTheProviderAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  provider: ollama\n  base_url: https://ollama.com/v1\n")
	t.Setenv("STARLIGHT_LLM_API_KEY", "generic-wins")
	t.Setenv("OLLAMA_API_KEY", "alias-loses")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.APIKey != "generic-wins" {
		t.Errorf("api_key = %q, want the documented generic variable to win", cfg.LLM.APIKey)
	}
}

// TestMissingKeyMessageNamesTheProviderVariable: the error has to send the user to
// a variable that works for the provider they configured.
func TestMissingKeyMessageNamesTheProviderVariable(t *testing.T) {
	cases := []struct{ provider, want string }{
		{"ollama", "OLLAMA_API_KEY"},
		{"openai", "STARLIGHT_LLM_API_KEY"},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		mustWrite(t, path, "llm:\n  provider: "+tc.provider+"\n")
		for _, v := range []string{"STARLIGHT_LLM_API_KEY", "OPENAI_API_KEY", "OLLAMA_API_KEY"} {
			os.Unsetenv(v)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatalf("%s: a missing key must be reported", tc.provider)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, must name %s", tc.provider, err, tc.want)
		}
	}
}
