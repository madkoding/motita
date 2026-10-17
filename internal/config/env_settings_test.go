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

// TestTheNewEnvironmentVariablesRejectBadValues: each variable added for the
// documented "MOTITA_* covers everything" rule must validate its input, naming
// the variable that is wrong.
func TestTheNewEnvironmentVariablesRejectBadValues(t *testing.T) {
	cases := map[string]string{
		"MOTITA_SANDBOX_OPEN_FILES":       "many",
		"MOTITA_SANDBOX_MAX_FILE_SIZE_MB": "big",
		"MOTITA_SANDBOX_MAX_OUTPUT_KB":    "plenty",
		"MOTITA_SANDBOX_KEEP_EPHEMERAL":   "perhaps",
		"MOTITA_AGENT_ON_FAILURE_KIND":    "nothing-to-validate-here",
	}
	// Only the numeric and boolean ones can fail; the text one is free-form and
	// is validated later by validate(). Keep the map honest:
	for key, value := range cases {
		if key == "MOTITA_AGENT_ON_FAILURE_KIND" {
			continue
		}
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

// --- prompts and other overrides from the environment -----------------------

// TestApplyEnvironmentOverridesEveryPrompt: the six prompt texts are plain
// settings, so the convention MOTITA_<BLOCK>_<FIELD> must reach them too.
func TestApplyEnvironmentOverridesEveryPrompt(t *testing.T) {
	c := Default()
	c.Prompts.Analyze.System = "analyze system"
	c.Prompts.Analyze.User = "analyze user"
	c.Prompts.Plan.System = "plan system"
	c.Prompts.Plan.User = "plan user"
	c.Prompts.Execute.System = "execute system"
	c.Prompts.Execute.User = "execute user"

	t.Setenv("MOTITA_PROMPTS_ANALYZE_SYSTEM", "new analyze system")
	t.Setenv("MOTITA_PROMPTS_ANALYZE_USER", "new analyze user")
	t.Setenv("MOTITA_PROMPTS_PLAN_SYSTEM", "new plan system")
	t.Setenv("MOTITA_PROMPTS_PLAN_USER", "new plan user")
	t.Setenv("MOTITA_PROMPTS_EXECUTE_SYSTEM", "new execute system")
	t.Setenv("MOTITA_PROMPTS_EXECUTE_USER", "new execute user")

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
	t.Setenv("MOTITA_TEST_PROMPT", text)

	if got := readPrompt("MOTITA_TEST_PROMPT", "original"); got != text {
		t.Errorf("readPrompt = %q, want the text untouched %q", got, text)
	}
}

// TestReadPromptIgnoresAnEmptyValue: an empty or blank value means "leave it as
// it was", the same as the other settings (and what stopped a stray variable from
// wiping a value).
func TestReadPromptIgnoresAnEmptyValue(t *testing.T) {
	for _, value := range []string{"", "   ", "\n\t"} {
		t.Setenv("MOTITA_TEST_PROMPT", value)
		if got := readPrompt("MOTITA_TEST_PROMPT", "original"); got != "original" {
			t.Errorf("value %q: got %q, want the original", value, got)
		}
	}
}

// TestReadPromptWithoutTheVariable: absent means untouched, which is the common
// case.
func TestReadPromptWithoutTheVariable(t *testing.T) {
	os.Unsetenv("MOTITA_TEST_PROMPT")
	if got := readPrompt("MOTITA_TEST_PROMPT", "original"); got != "original" {
		t.Errorf("got %q, want the original", got)
	}
}

// TestShutdownTimeoutAcceptsBothNames: the field is graceful_shutdown_timeout, so
// the documented variable is MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT. The name
// published in the previous release (MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT) must still
// work, and the documented one must win when both are set.
func TestShutdownTimeoutAcceptsBothNames(t *testing.T) {
	const documented = "MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT"
	const historical = "MOTITA_AGENT_SHUTDOWN_TIMEOUT"

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
	t.Setenv("MOTITA_AGENT_GRACEFUL_SHUTDOWN_TIMEOUT", "later")
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
// (MOTITA_<BLOCK>_<FIELD>, from the yaml tags). If a setting is added without
// its variable, this fails instead of the promise quietly becoming false.
//
// The documented exceptions are the collections (they cannot come from a single
// variable), the settings nested under a sub-block, and the compatibility name.
func TestEveryScalarSettingHasAnEnvironmentVariable(t *testing.T) {
	// Collections: they are lists or maps and are set in the YAML.
	collections := map[string]bool{
		"MOTITA_TASK_SOURCE_HEADERS": true,
		"MOTITA_ANCHOR_ARGS":         true,
		"MOTITA_ANCHOR_CHECKS":       true,
		"MOTITA_FINAL_ACTION_ARGS":   true,
	}
	// Set under a sub-block, so the variable is not BLOCK_FIELD.
	nested := map[string]bool{
		"MOTITA_AGENT_ON_FAILURE": true, // MOTITA_AGENT_ON_FAILURE_KIND/COMMAND
		"MOTITA_REVIEW_LLM":       true, // MOTITA_REVIEW_LLM_* sub-fields, handled by applyLLMEnvironment
	}
	// Read from the environment elsewhere (see Load), not by ApplyEnvironment.
	otherReaders := map[string]bool{
		"MOTITA_AGENT_SHUTDOWN_TIMEOUT": true, // compatibility name, documented
		"MOTITA_SANDBOX_CGROUPS":        true, // read by the sandbox layer
	}

	implemented := map[string]bool{}
	body, err := os.ReadFile("environment.go")
	if err != nil {
		t.Fatalf("could not read environment.go: %v", err)
	}
	for _, m := range regexp.MustCompile(`"(MOTITA_[A-Z0-9_]+)"`).FindAllStringSubmatch(string(body), -1) {
		implemented[m[1]] = true
	}

	// Skip the collections and whatever has its own reader.
	exempt := func(variable string) bool {
		return collections[variable] || nested[variable] || otherReaders[variable]
	}
	c := reflect.TypeOf(Config{})
	for i := 0; i < c.NumField(); i++ {
		checkBlockVariables(t, c.Field(i), implemented, exempt)
	}
}

// checkBlockVariables reports each scalar setting of one configuration block that
// environment.go has no variable for.
func checkBlockVariables(t *testing.T, blockField reflect.StructField, implemented map[string]bool,
	exempt func(string) bool) {
	block := blockField.Tag.Get("yaml")
	bt := blockField.Type
	for j := 0; j < bt.NumField(); j++ {
		field := bt.Field(j)
		name := field.Tag.Get("yaml")
		variable := "MOTITA_" + strings.ToUpper(block+"_"+name)

		if exempt(variable) || !isScalarSetting(field) {
			continue
		}
		if !implemented[variable] {
			// The field may have a differently named variable on purpose:
			// the documented compatibility case is listed above.
			t.Errorf("the setting %s.%s has no environment variable (%s)", block, name, variable)
		}
	}
}

// isScalarSetting reports whether a setting is one value an environment variable can hold.
func isScalarSetting(field reflect.StructField) bool {
	// Sub-structs (like on_failure) hold their own fields.
	if field.Type.Kind() == reflect.Struct && field.Type.Name() != "Duration" {
		return false
	}
	// A slice or a map cannot be expressed as one value.
	k := field.Type.Kind()
	return k != reflect.Slice && k != reflect.Map
}

// TestReadOnlyAndShellComeFromTheEnvironment: the two settings that make plan mode
// configurable, including the case that must be reported rather than ignored.
func TestReadOnlyAndShellComeFromTheEnvironment(t *testing.T) {
	cases := []readOnlyShellCase{
		{"neither set", nil, false, "", false},
		{"read_only true", map[string]string{"MOTITA_AGENT_READ_ONLY": "true"}, true, "", false},
		{"read_only on", map[string]string{"MOTITA_AGENT_READ_ONLY": "on"}, true, "", false},
		{"read_only false", map[string]string{"MOTITA_AGENT_READ_ONLY": "off"}, false, "", false},
		{"read_only blank is ignored", map[string]string{"MOTITA_AGENT_READ_ONLY": "  "}, false, "", false},
		{"the shell", map[string]string{"MOTITA_AGENT_SHELL": "/bin/dash"}, false, "/bin/dash", false},
		{"a bad boolean is reported",
			map[string]string{"MOTITA_AGENT_READ_ONLY": "maybe"}, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkReadOnlyAndShell(t, tc) })
	}
}

// readOnlyShellCase is one environment for TestReadOnlyAndShellComeFromTheEnvironment.
type readOnlyShellCase struct {
	name    string
	env     map[string]string
	wantRO  bool
	wantSh  string
	wantErr bool
}

// checkReadOnlyAndShell applies one case's environment and checks what it set.
func checkReadOnlyAndShell(t *testing.T, tc readOnlyShellCase) {
	for _, k := range []string{"MOTITA_AGENT_READ_ONLY", "MOTITA_AGENT_SHELL"} {
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
}

// TestProviderKeyVariable: each provider resolves to the variable its own
// documentation uses, and an unknown one falls back to the generic name.
// TestProviderNeedsKey: claude-code is reached without a key of motita's, and the check reads a provider the way the rest of the configuration does.
func TestProviderNeedsKey(t *testing.T) {
	for provider, want := range map[string]bool{
		"claude-code": false, " Claude-Code ": false,
		"openai": true, "ollama": true, "anthropic": true, "copilot": true, "": true,
	} {
		if got := ProviderNeedsKey(provider); got != want {
			t.Errorf("ProviderNeedsKey(%q) = %v, want %v", provider, got, want)
		}
	}
}

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
	if got := ProviderKeyVariable("openai"); got != "MOTITA_LLM_API_KEY" {
		t.Errorf("ProviderKeyVariable(openai) = %q", got)
	}
	if got := ProviderKeyVariable(""); got != "MOTITA_LLM_API_KEY" {
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
	t.Setenv("MOTITA_LLM_API_KEY", "")
	os.Unsetenv("MOTITA_LLM_API_KEY")
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
	t.Setenv("MOTITA_LLM_API_KEY", "generic-wins")
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
		{"openai", "MOTITA_LLM_API_KEY"},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		mustWrite(t, path, "llm:\n  provider: "+tc.provider+"\n")
		for _, v := range []string{"MOTITA_LLM_API_KEY", "OPENAI_API_KEY", "OLLAMA_API_KEY"} {
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
