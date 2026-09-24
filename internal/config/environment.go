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
// Convention: MOTITA_<BLOCK>_<FIELD>. They win over the YAML, which is what
// is expected when injecting secrets into a container or a systemd service.
//
// The bindings are TABLES, not a long list of assignments, because the shape of
// this code is the shape of a bug: with one statement per variable, a variable
// added to the README and forgotten here is silently ignored in the one path —
// the container, the systemd unit — where nobody can check by hand. A table is
// read as a whole, and a missing row is visible.
// ---------------------------------------------------------------------------

// binding maps a variable name to the field it overwrites.
type binding[T any] struct {
	key string
	dst *T
}

// textBinding is the verbatim case: the value is trimmed, and a variable that is
// set but blank leaves the field as it was.
func textBindings(c *Config) []binding[string] {
	return []binding[string]{
		{"MOTITA_TASK_SOURCE_KIND", &c.TaskSource.Kind},
		{"MOTITA_TASK_SOURCE_PATH", &c.TaskSource.Path},
		{"MOTITA_TASK_SOURCE_DIR", &c.TaskSource.Dir},
		{"MOTITA_TASK_SOURCE_URL", &c.TaskSource.URL},
		{"MOTITA_TASK_SOURCE_METHOD", &c.TaskSource.Method},
		{"MOTITA_TASK_SOURCE_FIELD", &c.TaskSource.Field},
		{"MOTITA_TASK_SOURCE_BODY", &c.TaskSource.Body},

		{"MOTITA_ANCHOR_KIND", &c.Anchor.Kind},
		{"MOTITA_ANCHOR_COMMAND", &c.Anchor.Command},
		{"MOTITA_ANCHOR_EXPECT_OUTPUT", &c.Anchor.ExpectOutput},

		{"MOTITA_SANDBOX_KIND", &c.Sandbox.Kind},
		{"MOTITA_SANDBOX_ROOT", &c.Sandbox.Root},
		{"MOTITA_SANDBOX_USER", &c.Sandbox.User},
		{"MOTITA_SANDBOX_CGROUPS", &c.Sandbox.Cgroups},
		{"MOTITA_SANDBOX_CGROUP_ROOT", &c.Sandbox.CgroupRoot},

		{"MOTITA_SKILLS_DIR", &c.Skills.Dir},

		{"MOTITA_GATEWAY_LISTEN", &c.Gateway.Listen},
		{"MOTITA_GATEWAY_TOKEN_FILE", &c.Gateway.TokenFile},

		{"MOTITA_FINAL_ACTION_KIND", &c.FinalAction.Kind},
		{"MOTITA_FINAL_ACTION_COMMAND", &c.FinalAction.Command},
		{"MOTITA_FINAL_ACTION_URL", &c.FinalAction.URL},
		{"MOTITA_FINAL_ACTION_METHOD", &c.FinalAction.Method},
		{"MOTITA_FINAL_ACTION_COMMIT_MESSAGE", &c.FinalAction.CommitMessage},

		{"MOTITA_AGENT_WORKSPACE_DIR", &c.Agent.WorkspaceDir},
		{"MOTITA_AGENT_LOG_FILE", &c.Agent.LogFile},
		{"MOTITA_AGENT_LOG_LEVEL", &c.Agent.LogLevel},
		{"MOTITA_AGENT_SHELL", &c.Agent.Shell},

		{"MOTITA_AGENT_ON_FAILURE_KIND", &c.Agent.OnFailure.Kind},
		{"MOTITA_AGENT_ON_FAILURE_COMMAND", &c.Agent.OnFailure.Command},
	}
}

// promptBindings are the prompt texts. They are read with readPrompt, which does
// not trim: a prompt is multi-line and its layout is part of the instructions.
func promptBindings(c *Config) []binding[string] {
	return []binding[string]{
		{"MOTITA_PROMPTS_ANALYZE_SYSTEM", &c.Prompts.Analyze.System},
		{"MOTITA_PROMPTS_ANALYZE_USER", &c.Prompts.Analyze.User},
		{"MOTITA_PROMPTS_PLAN_SYSTEM", &c.Prompts.Plan.System},
		{"MOTITA_PROMPTS_PLAN_USER", &c.Prompts.Plan.User},
		{"MOTITA_PROMPTS_EXECUTE_SYSTEM", &c.Prompts.Execute.System},
		{"MOTITA_PROMPTS_EXECUTE_USER", &c.Prompts.Execute.User},
	}
}

func durationBindings(c *Config) []binding[time.Duration] {
	return []binding[time.Duration]{
		{"MOTITA_TASK_SOURCE_INTERVAL", &c.TaskSource.Interval},
		{"MOTITA_ANCHOR_TIMEOUT", &c.Anchor.Timeout},
		{"MOTITA_SANDBOX_TIMEOUT", &c.Sandbox.Timeout},
		{"MOTITA_LLM_TIMEOUT", &c.LLM.Timeout},
		{"MOTITA_LLM_BACKOFF_INITIAL", &c.LLM.BackoffInitial},
		{"MOTITA_LLM_BACKOFF_MAX", &c.LLM.BackoffMax},
	}
}

func integerBindings(c *Config) []binding[int] {
	return []binding[int]{
		{"MOTITA_ANCHOR_EXPECT_EXIT", &c.Anchor.ExpectExit},
		{"MOTITA_SANDBOX_MEMORY_MB", &c.Sandbox.MemoryMB},
		{"MOTITA_SANDBOX_CPU_SECONDS", &c.Sandbox.CPUSeconds},
		{"MOTITA_SANDBOX_PROCESSES", &c.Sandbox.Processes},
		{"MOTITA_SANDBOX_OPEN_FILES", &c.Sandbox.OpenFiles},
		{"MOTITA_SANDBOX_MAX_FILE_SIZE_MB", &c.Sandbox.MaxFileSizeMB},
		{"MOTITA_SANDBOX_MAX_OUTPUT_KB", &c.Sandbox.MaxOutputKB},
		{"MOTITA_SKILLS_MAX_FILE_BYTES", &c.Skills.MaxFileBytes},
		{"MOTITA_GATEWAY_MAX_BODY_KB", &c.Gateway.MaxBodyKB},
		{"MOTITA_GATEWAY_MAX_SESSIONS", &c.Gateway.MaxSessions},
		{"MOTITA_LLM_MAX_TOKENS", &c.LLM.MaxTokens},
		{"MOTITA_LLM_SESSION_CONTEXT_WINDOW", &c.LLM.Session.ContextWindow},
		{"MOTITA_LLM_SESSION_RESERVE", &c.LLM.Session.Reserve},
		{"MOTITA_LLM_SESSION_KEEP_RECENT", &c.LLM.Session.KeepRecent},
		{"MOTITA_LLM_MAX_ATTEMPTS", &c.LLM.MaxAttempts},
		{"MOTITA_AGENT_MAX_RETRIES", &c.Agent.MaxRetries},
		{"MOTITA_AGENT_SUBTASK_DEPTH", &c.Agent.SubtaskDepth},
		{"MOTITA_AGENT_MAX_TASKS", &c.Agent.MaxTasks},
		{"MOTITA_AGENT_LOG_MAX_MB", &c.Agent.LogMaxMB},
		{"MOTITA_AGENT_LOG_BACKUPS", &c.Agent.LogBackups},
	}
}

func boolBindings(c *Config) []binding[bool] {
	return []binding[bool]{
		{"MOTITA_SANDBOX_KEEP_EPHEMERAL", &c.Sandbox.KeepEphemeral},
		{"MOTITA_SANDBOX_ISOLATE_NETWORK", &c.Sandbox.IsolateNetwork},
		{"MOTITA_AGENT_LOG_CONSOLE", &c.Agent.LogConsole},
		{"MOTITA_AGENT_READ_ONLY", &c.Agent.ReadOnly},
		{"MOTITA_AGENT_POLICY_ENFORCE", &c.Agent.Policy.Enforce},
		{"MOTITA_AGENT_POLICY_STRICT", &c.Agent.Policy.Strict},
		{"MOTITA_GATEWAY_ENABLED", &c.Gateway.Enabled},
		{"MOTITA_GATEWAY_WEBUI", &c.Gateway.WebUI},
	}
}

// ApplyEnvironment overlays the MOTITA_* variables on the configuration.
func ApplyEnvironment(c *Config) error {
	for _, b := range textBindings(c) {
		*b.dst = readText(b.key, *b.dst)
	}
	applyListEnvironment(c)
	for _, b := range promptBindings(c) {
		*b.dst = readPrompt(b.key, *b.dst)
	}
	for _, b := range durationBindings(c) {
		v, err := readDuration(b.key, *b.dst)
		if err != nil {
			return err
		}
		*b.dst = v
	}
	for _, b := range integerBindings(c) {
		v, err := readInteger(b.key, *b.dst)
		if err != nil {
			return err
		}
		*b.dst = v
	}
	for _, b := range boolBindings(c) {
		v, err := readBool(b.key, *b.dst)
		if err != nil {
			return err
		}
		*b.dst = v
	}
	return applyLLMEnvironment(c)
}

// applyLLMEnvironment handles the LLM block, whose precedence rules do not fit a
// table: the variable of the provider itself and the standard OpenAI names are
// consulted only when nothing else set the value, so a configuration that names
// its own endpoint is never overridden by a stray variable.
//
// The order is: the documented MOTITA_ name, then the provider's own alias,
// then the standard OpenAI name.
func applyLLMEnvironment(c *Config) error {
	c.LLM.Provider = readText("MOTITA_LLM_PROVIDER", c.LLM.Provider)

	// The provider's key variable, so "just paste the key" works the way each
	// service documents it (OLLAMA_API_KEY for Ollama Cloud).
	c.LLM.APIKey = readText("MOTITA_LLM_API_KEY", c.LLM.APIKey)
	if _, generic := os.LookupEnv("MOTITA_LLM_API_KEY"); !generic {
		c.LLM.APIKey = readText(ProviderKeyVariable(c.LLM.Provider), c.LLM.APIKey)
	}
	// The names the OpenAI ecosystem already exports, which the README promises to
	// accept. They are applied HERE, in the one overlay that every loading path
	// runs — reading them only inside Load left the -p and TUI paths, which load the
	// defaults plus the environment, with no key at all.
	if _, explicit := os.LookupEnv("MOTITA_LLM_API_KEY"); !explicit {
		if _, own := os.LookupEnv(ProviderKeyVariable(c.LLM.Provider)); !own {
			c.LLM.APIKey = readText("OPENAI_API_KEY", c.LLM.APIKey)
		}
	}

	// The model and the endpoint are only taken from the standard OpenAI names when
	// each is still the default, which is the case a bare `OPENAI_BASE_URL=...` is
	// written for.
	c.LLM.Model = readText("MOTITA_LLM_MODEL", c.LLM.Model)
	if os.Getenv("MOTITA_LLM_MODEL") == "" && c.LLM.Model == Default().LLM.Model {
		c.LLM.Model = readText("OPENAI_MODEL", c.LLM.Model)
	}
	c.LLM.BaseURL = readText("MOTITA_LLM_BASE_URL", c.LLM.BaseURL)
	if os.Getenv("MOTITA_LLM_BASE_URL") == "" && c.LLM.BaseURL == Default().LLM.BaseURL {
		c.LLM.BaseURL = readText("OPENAI_BASE_URL", c.LLM.BaseURL)
	}

	// The two floating-point settings have their own variables because they are the
	// only non-integer numbers in the configuration.
	for _, b := range []binding[float64]{
		{"MOTITA_LLM_SESSION_COMPACT_AT", &c.LLM.Session.CompactAt},
		{"MOTITA_LLM_TEMPERATURE", &c.LLM.Temperature},
	} {
		if v := os.Getenv(b.key); v != "" {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return fmt.Errorf("%s: %q is not a number", b.key, v)
			}
			*b.dst = f
		}
	}

	if err := applyReasoningEnvironment(c); err != nil {
		return err
	}

	// The shutdown timeout has a second, historical name: it was published while the
	// field is called graceful_shutdown_timeout, so a reader following the documented
	// formula would have set a variable that did nothing.
	v, err := readDurationAliased(
		"MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT", // the documented name
		"MOTITA_AGENT_SHUTDOWN_TIMEOUT",          // kept for compatibility
		c.Agent.ShutdownTimeout)
	if err != nil {
		return err
	}
	c.Agent.ShutdownTimeout = v
	return nil
}

// applyReasoningEnvironment reads the reasoning pair, which is two fields that must
// agree: a level of "off" with reasoning enabled would mean nothing, and a level
// other than "off" with reasoning disabled would be a setting nobody applied.
func applyReasoningEnvironment(c *Config) error {
	if v := strings.ToLower(readText("MOTITA_LLM_REASONING_ENABLED", "")); v != "" {
		c.LLM.Reasoning.Enabled = v == "true" || v == "yes" || v == "1" || v == "on"
	}
	v := strings.ToLower(readText("MOTITA_LLM_REASONING_LEVEL", c.LLM.Reasoning.Level))
	switch v {
	case "", "off", "low", "medium", "high":
		if v != "" {
			c.LLM.Reasoning.Level = v
		}
		return nil
	default:
		return fmt.Errorf("MOTITA_LLM_REASONING_LEVEL must be one of: off, low, medium, high")
	}
}

// applyListEnvironment reads the settings that are a LIST of strings rather than one value.
//
// They cannot go through the binding tables above, whose shape is "one variable, one field of a
// scalar type". Only gateway.allow is one today, and it is named explicitly rather than given a
// generic table so that a second list setting is a decision (how is it split? by comma? by space?)
// and not an accident of someone reaching for a helper.
//
// The separator is a COMMA and the entries are trimmed. A comma rather than a space because a rule
// may be `192.168.0.0/16` or `!any`, and neither contains one - while a space is what somebody
// would type between rules by habit, so a space-separated list is accepted as well rather than
// being silently read as one malformed rule.
//
// An EMPTY or blank variable leaves the field alone, exactly like every other readText binding:
// exporting MOTITA_GATEWAY_ALLOW= in a compose file because a secret did not arrive must not
// wipe a restriction the operator wrote in their YAML. Clearing a rule list is done by editing the
// file, not by an empty variable.
func applyListEnvironment(c *Config) {
	v, ok := os.LookupEnv("MOTITA_GATEWAY_ALLOW")
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	c.Gateway.Allow = splitRules(v)
}

// splitRules turns a variable's value into rule specs.
func splitRules(v string) []string {
	out := []string{}
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// readText returns the value of the variable, or the current one when it is not
// defined.
//
// A variable that is defined but blank ("   ") is treated as not defined: in
// CI, in systemd or in a docker-compose it is very common to export an empty
// variable because the secret did not arrive. Without this check,
// `MOTITA_LLM_MODEL="   "` wiped the model instead of leaving it as it was.
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
// documented name (MOTITA_<BLOCK>_<FIELD>, the field's yaml tag) always wins;
// the older name is still honoured so a deployment written against the previous
// release keeps working.
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

// readBool reads a boolean setting. The accepted spellings mirror what systemd and the shell
// already use, so a value that works in one place works here too.
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
	"ollama":  "OLLAMA_API_KEY",
	"copilot": "GITHUB_COPILOT_TOKEN",
}

// ProviderKeyVariable returns the provider-specific variable for a key, or the
// generic one when the provider has no alias of its own.
func ProviderKeyVariable(provider string) string {
	if v, ok := providerKeyAliases[strings.ToLower(strings.TrimSpace(provider))]; ok {
		return v
	}
	return "MOTITA_LLM_API_KEY"
}
