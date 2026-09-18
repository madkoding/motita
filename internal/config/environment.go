package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Environment variable overlay.
//
// Convention: STARLIGHT_<BLOCK>_<FIELD>. They win over the YAML, which is what
// is expected when injecting secrets into a container or a systemd service.
// ---------------------------------------------------------------------------

// ApplyEnvironment overlays the STARLIGHT_* variables on the configuration.
func ApplyEnvironment(c *Config) error {
	var err error

	// --- task_source ---
	c.TaskSource.Kind = readText("STARLIGHT_TASK_SOURCE_KIND", c.TaskSource.Kind)
	c.TaskSource.Path = readText("STARLIGHT_TASK_SOURCE_PATH", c.TaskSource.Path)
	c.TaskSource.Dir = readText("STARLIGHT_TASK_SOURCE_DIR", c.TaskSource.Dir)
	c.TaskSource.URL = readText("STARLIGHT_TASK_SOURCE_URL", c.TaskSource.URL)
	c.TaskSource.Method = readText("STARLIGHT_TASK_SOURCE_METHOD", c.TaskSource.Method)
	c.TaskSource.Field = readText("STARLIGHT_TASK_SOURCE_FIELD", c.TaskSource.Field)
	c.TaskSource.Body = readText("STARLIGHT_TASK_SOURCE_BODY", c.TaskSource.Body)
	if c.TaskSource.Interval, err = readDuration("STARLIGHT_TASK_SOURCE_INTERVAL", c.TaskSource.Interval); err != nil {
		return err
	}

	// --- anchor ---
	c.Anchor.Kind = readText("STARLIGHT_ANCHOR_KIND", c.Anchor.Kind)
	c.Anchor.Command = readText("STARLIGHT_ANCHOR_COMMAND", c.Anchor.Command)
	if c.Anchor.Timeout, err = readDuration("STARLIGHT_ANCHOR_TIMEOUT", c.Anchor.Timeout); err != nil {
		return err
	}
	if c.Anchor.ExpectExit, err = readInteger("STARLIGHT_ANCHOR_EXPECT_EXIT", c.Anchor.ExpectExit); err != nil {
		return err
	}
	c.Anchor.ExpectOutput = readText("STARLIGHT_ANCHOR_EXPECT_OUTPUT", c.Anchor.ExpectOutput)

	// --- sandbox ---
	c.Sandbox.Kind = readText("STARLIGHT_SANDBOX_KIND", c.Sandbox.Kind)
	c.Sandbox.Root = readText("STARLIGHT_SANDBOX_ROOT", c.Sandbox.Root)
	c.Sandbox.User = readText("STARLIGHT_SANDBOX_USER", c.Sandbox.User)
	c.Sandbox.Cgroups = readText("STARLIGHT_SANDBOX_CGROUPS", c.Sandbox.Cgroups)
	c.Sandbox.CgroupRoot = readText("STARLIGHT_SANDBOX_CGROUP_ROOT", c.Sandbox.CgroupRoot)
	if c.Sandbox.MemoryMB, err = readInteger("STARLIGHT_SANDBOX_MEMORY_MB", c.Sandbox.MemoryMB); err != nil {
		return err
	}
	if c.Sandbox.CPUSeconds, err = readInteger("STARLIGHT_SANDBOX_CPU_SECONDS", c.Sandbox.CPUSeconds); err != nil {
		return err
	}
	if c.Sandbox.Processes, err = readInteger("STARLIGHT_SANDBOX_PROCESSES", c.Sandbox.Processes); err != nil {
		return err
	}
	if c.Sandbox.OpenFiles, err = readInteger("STARLIGHT_SANDBOX_OPEN_FILES", c.Sandbox.OpenFiles); err != nil {
		return err
	}
	if c.Sandbox.MaxFileSizeMB, err = readInteger("STARLIGHT_SANDBOX_MAX_FILE_SIZE_MB", c.Sandbox.MaxFileSizeMB); err != nil {
		return err
	}
	if c.Sandbox.MaxOutputKB, err = readInteger("STARLIGHT_SANDBOX_MAX_OUTPUT_KB", c.Sandbox.MaxOutputKB); err != nil {
		return err
	}
	if c.Sandbox.KeepEphemeral, err = readBool("STARLIGHT_SANDBOX_KEEP_EPHEMERAL", c.Sandbox.KeepEphemeral); err != nil {
		return err
	}
	if c.Sandbox.Timeout, err = readDuration("STARLIGHT_SANDBOX_TIMEOUT", c.Sandbox.Timeout); err != nil {
		return err
	}
	if c.Sandbox.IsolateNetwork, err = readBool("STARLIGHT_SANDBOX_ISOLATE_NETWORK", c.Sandbox.IsolateNetwork); err != nil {
		return err
	}

	// --- llm ---
	c.LLM.Provider = readText("STARLIGHT_LLM_PROVIDER", c.LLM.Provider)
	c.LLM.Model = readText("STARLIGHT_LLM_MODEL", c.LLM.Model)
	c.LLM.APIKey = readText("STARLIGHT_LLM_API_KEY", c.LLM.APIKey)
	// The provider's own variable is honoured too, so "just paste the key" works
	// the way each service documents it (OLLAMA_API_KEY for Ollama Cloud). It is
	// consulted only when the documented generic variable is absent, so the
	// documented name wins when both are present.
	if _, generic := os.LookupEnv("STARLIGHT_LLM_API_KEY"); !generic {
		c.LLM.APIKey = readText(ProviderKeyVariable(c.LLM.Provider), c.LLM.APIKey)
	}
	c.LLM.BaseURL = readText("STARLIGHT_LLM_BASE_URL", c.LLM.BaseURL)
	if c.LLM.MaxTokens, err = readInteger("STARLIGHT_LLM_MAX_TOKENS", c.LLM.MaxTokens); err != nil {
		return err
	}
	if v := os.Getenv("STARLIGHT_LLM_TEMPERATURE"); v != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return fmt.Errorf("STARLIGHT_LLM_TEMPERATURE: %q is not a number", v)
		}
		c.LLM.Temperature = f
	}
	if c.LLM.Timeout, err = readDuration("STARLIGHT_LLM_TIMEOUT", c.LLM.Timeout); err != nil {
		return err
	}
	if c.LLM.MaxAttempts, err = readInteger("STARLIGHT_LLM_MAX_ATTEMPTS", c.LLM.MaxAttempts); err != nil {
		return err
	}
	if c.LLM.BackoffInitial, err = readDuration("STARLIGHT_LLM_BACKOFF_INITIAL", c.LLM.BackoffInitial); err != nil {
		return err
	}
	if c.LLM.BackoffMax, err = readDuration("STARLIGHT_LLM_BACKOFF_MAX", c.LLM.BackoffMax); err != nil {
		return err
	}

	// --- final_action ---
	c.FinalAction.Kind = readText("STARLIGHT_FINAL_ACTION_KIND", c.FinalAction.Kind)
	c.FinalAction.Command = readText("STARLIGHT_FINAL_ACTION_COMMAND", c.FinalAction.Command)
	c.FinalAction.URL = readText("STARLIGHT_FINAL_ACTION_URL", c.FinalAction.URL)
	c.FinalAction.Method = readText("STARLIGHT_FINAL_ACTION_METHOD", c.FinalAction.Method)
	c.FinalAction.CommitMessage = readText("STARLIGHT_FINAL_ACTION_COMMIT_MESSAGE", c.FinalAction.CommitMessage)

	// --- prompts ---
	// The prompt texts can be replaced from the environment too: it is how a
	// deployment adjusts the instructions of a frozen image without rebuilding it.
	c.Prompts.Analyze.System = readPrompt("STARLIGHT_PROMPTS_ANALYZE_SYSTEM", c.Prompts.Analyze.System)
	c.Prompts.Analyze.User = readPrompt("STARLIGHT_PROMPTS_ANALYZE_USER", c.Prompts.Analyze.User)
	c.Prompts.Plan.System = readPrompt("STARLIGHT_PROMPTS_PLAN_SYSTEM", c.Prompts.Plan.System)
	c.Prompts.Plan.User = readPrompt("STARLIGHT_PROMPTS_PLAN_USER", c.Prompts.Plan.User)
	c.Prompts.Execute.System = readPrompt("STARLIGHT_PROMPTS_EXECUTE_SYSTEM", c.Prompts.Execute.System)
	c.Prompts.Execute.User = readPrompt("STARLIGHT_PROMPTS_EXECUTE_USER", c.Prompts.Execute.User)

	// --- agent ---
	if c.Agent.MaxRetries, err = readInteger("STARLIGHT_AGENT_MAX_RETRIES", c.Agent.MaxRetries); err != nil {
		return err
	}
	if c.Agent.SubtaskDepth, err = readInteger("STARLIGHT_AGENT_SUBTASK_DEPTH", c.Agent.SubtaskDepth); err != nil {
		return err
	}
	if c.Agent.MaxTasks, err = readInteger("STARLIGHT_AGENT_MAX_TASKS", c.Agent.MaxTasks); err != nil {
		return err
	}
	c.Agent.WorkspaceDir = readText("STARLIGHT_AGENT_WORKSPACE_DIR", c.Agent.WorkspaceDir)
	c.Agent.LogFile = readText("STARLIGHT_AGENT_LOG_FILE", c.Agent.LogFile)
	c.Agent.LogLevel = readText("STARLIGHT_AGENT_LOG_LEVEL", c.Agent.LogLevel)
	if c.Agent.LogConsole, err = readBool("STARLIGHT_AGENT_LOG_CONSOLE", c.Agent.LogConsole); err != nil {
		return err
	}
	if c.Agent.LogMaxMB, err = readInteger("STARLIGHT_AGENT_LOG_MAX_MB", c.Agent.LogMaxMB); err != nil {
		return err
	}
	if c.Agent.LogBackups, err = readInteger("STARLIGHT_AGENT_LOG_BACKUPS", c.Agent.LogBackups); err != nil {
		return err
	}
	if c.Agent.ShutdownTimeout, err = readDurationAliased(
		"STARLIGHT_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT", // the documented name
		"STARLIGHT_AGENT_SHUTDOWN_TIMEOUT",          // kept for compatibility
		c.Agent.ShutdownTimeout); err != nil {
		return err
	}

	// --- agent (read-only plan mode and the shell used for actions) ---
	if c.Agent.ReadOnly, err = readBool("STARLIGHT_AGENT_READ_ONLY", c.Agent.ReadOnly); err != nil {
		return err
	}
	c.Agent.Shell = readText("STARLIGHT_AGENT_SHELL", c.Agent.Shell)

	// --- agent.on_failure ---
	c.Agent.OnFailure.Kind = readText("STARLIGHT_AGENT_ON_FAILURE_KIND", c.Agent.OnFailure.Kind)
	c.Agent.OnFailure.Command = readText("STARLIGHT_AGENT_ON_FAILURE_COMMAND", c.Agent.OnFailure.Command)

	return nil
}

// readText returns the value of the variable, or the current one when it is not
// defined.
//
// A variable that is defined but blank ("   ") is treated as not defined: in
// CI, in systemd or in a docker-compose it is very common to export an empty
// variable because the secret did not arrive. Without this check,
// `STARLIGHT_LLM_MODEL="   "` wiped the model instead of leaving it as it was.
func readText(key, current string) string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return current
	}
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return current
	}
	return trimmed
}

// readPrompt returns the value of the variable as written. Unlike readText it does
// not trim the text: a prompt is multi-line, its layout is part of the
// instructions, and collapsing it would change what the model receives. Only an
// empty or blank value is ignored, which means "leave it as it was".
func readPrompt(key, current string) string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return current
	}
	return v
}

func readDuration(key string, current time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return current, nil
	}
	// toDuration already names the key in its error, so it is not repeated here.
	d, err := toDuration(strings.TrimSpace(v), key)
	if err != nil {
		return current, err
	}
	return d, nil
}

// readDurationAliased reads a duration that has a second, historical name. The
// documented name (STARLIGHT_<BLOCK>_<FIELD>, the field's yaml tag) always wins;
// the older name is still honoured so a deployment written against the previous
// release keeps working. This exists because STARLIGHT_AGENT_SHUTDOWN_TIMEOUT was
// published while the field is called graceful_shutdown_timeout, so a reader
// following the documented formula would have set a variable that did nothing.
func readDurationAliased(primary, alias string, current time.Duration) (time.Duration, error) {
	v, err := readDuration(primary, current)
	if err != nil {
		return current, err
	}
	// Only consult the alias when the primary did not change the value.
	if v != current {
		return v, nil
	}
	return readDuration(alias, current)
}

func readInteger(key string, current int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return current, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return current, fmt.Errorf("%s: %q is not an integer", key, v)
	}
	return n, nil
}

func readBool(key string, current bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return current, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return current, fmt.Errorf("%s: %q is not a boolean (use true/false)", key, v)
	}
}

// providerKeyAliases maps a provider id to the environment variable its own
// documentation uses. Keeping the table here means the wizard, the loader and the
// tests all agree on one name per provider.
var providerKeyAliases = map[string]string{
	"ollama": "OLLAMA_API_KEY",
}

// ProviderKeyVariable returns the provider-specific variable for a key, or the
// generic one when the provider has no alias of its own.
func ProviderKeyVariable(provider string) string {
	if v, ok := providerKeyAliases[strings.ToLower(strings.TrimSpace(provider))]; ok {
		return v
	}
	return "STARLIGHT_LLM_API_KEY"
}
