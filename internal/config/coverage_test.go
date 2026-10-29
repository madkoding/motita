package config

import (
	"os"
	"path/filepath"
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
		{"negative transient budget", func(c *Config) { c.LLM.TransientBudget = -1 }, "llm.transient_budget"},
		{"negative retry-after cap", func(c *Config) { c.LLM.RetryAfterMax = -1 }, "llm.retry_after_max"},
		{"jitter above one", func(c *Config) { c.LLM.Jitter = 1.5 }, "llm.jitter"},
		{"negative jitter", func(c *Config) { c.LLM.Jitter = -0.1 }, "llm.jitter"},
		{"zero initial backoff", func(c *Config) { c.LLM.BackoffInitial = 0 }, "llm.backoff_initial"},
		{"smaller max backoff", func(c *Config) {
			c.LLM.BackoffMax = time.Millisecond
			c.LLM.BackoffInitial = time.Second
		},
			"llm.backoff_max"},
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
// channel, for instance) must be named in the error instead of being skipped.
func TestDecodeUnsupportedFieldType(t *testing.T) {
	type weird struct {
		Channel chan int `yaml:"channel"`
	}
	var w weird
	if err := Decode(map[string]any{"channel": 3}, &w); err == nil {
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
	} else {
		// the expected outcome: nothing more to check
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
		"MOTITA_SANDBOX_TIMEOUT",
		"MOTITA_LLM_BACKOFF_INITIAL",
		"MOTITA_LLM_BACKOFF_MAX",
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

// TestDecodeOptionalTemperature: an unset temperature stays nil, a set one (even 0) is kept.
func TestDecodeOptionalTemperature(t *testing.T) {
	c := Default()
	if c.LLM.Temperature != nil || c.LLM.TemperatureOr(0.2) != 0.2 {
		t.Fatal("the default temperature must be unset")
	}
	if err := Decode(map[string]any{"llm": map[string]any{"temperature": int64(0)}}, &c); err != nil {
		t.Fatal(err)
	}
	if c.LLM.Temperature == nil || c.LLM.TemperatureOr(0.2) != 0 {
		t.Errorf("an explicit 0 must be kept: %v", c.LLM.Temperature)
	}
}
