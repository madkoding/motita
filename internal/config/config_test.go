package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseYAMLBasic(t *testing.T) {
	data := []byte(`
# comment
name: agent
version: 2
ratio: 0.5
active: true
disabled: false
nothing: ~
list:
  - one
  - two
inline: [a, b, c]
map:
  key: value
  nested:
    deep: yes
inline_map: {x: 1, y: 2}
`)
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	if m["name"] != "agent" {
		t.Errorf("name = %v", m["name"])
	}
	if m["version"] != int64(2) {
		t.Errorf("version = %v (%T)", m["version"], m["version"])
	}
	if m["ratio"] != 0.5 {
		t.Errorf("ratio = %v (%T)", m["ratio"], m["ratio"])
	}
	if m["active"] != true || m["disabled"] != false {
		t.Errorf("booleans wrong: active=%v disabled=%v", m["active"], m["disabled"])
	}
	if m["nothing"] != nil {
		t.Errorf("~ should be nil, it is %v", m["nothing"])
	}
	list, ok := m["list"].([]any)
	if !ok || len(list) != 2 || list[0] != "one" {
		t.Errorf("list = %#v", m["list"])
	}
	inline, ok := m["inline"].([]any)
	if !ok || len(inline) != 3 {
		t.Errorf("inline = %#v", m["inline"])
	}
	nestedMap, ok := m["map"].(map[string]any)
	if !ok {
		t.Fatalf("map = %#v", m["map"])
	}
	nested, ok := nestedMap["nested"].(map[string]any)
	if !ok || nested["deep"] != "yes" {
		t.Errorf("map.nested = %#v", nestedMap["nested"])
	}
	if m["inline_map"].(map[string]any)["x"] != int64(1) {
		t.Errorf("inline_map = %#v", m["inline_map"])
	}
}

func TestParseYAMLQuotesAndComments(t *testing.T) {
	data := []byte(`
single: 'with # hash inside'
double: "with \"escape\" and newline\n"
with_comment: value   # this is ignored
empty: ""
`)
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	if m["single"] != "with # hash inside" {
		t.Errorf("single = %q", m["single"])
	}
	if m["double"] != "with \"escape\" and newline\n" {
		t.Errorf("double = %q", m["double"])
	}
	if m["with_comment"] != "value" {
		t.Errorf("with_comment = %q", m["with_comment"])
	}
	if m["empty"] != "" {
		t.Errorf("empty = %q", m["empty"])
	}
}

func TestParseYAMLBlockScalars(t *testing.T) {
	data := []byte(`
literal: |
  line one
  line two

  after blank
folded: >
  this gets
  joined into one line
no_newline: |-
  no trailing newline
`)
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	literal, _ := m["literal"].(string)
	if !strings.Contains(literal, "line one\nline two") {
		t.Errorf("literal = %q", literal)
	}
	if !strings.Contains(literal, "after blank") {
		t.Errorf("the line after the blank one was lost: %q", literal)
	}
	folded, _ := m["folded"].(string)
	if !strings.Contains(folded, "this gets joined into one line") {
		t.Errorf("folded = %q", folded)
	}
	noNewline, _ := m["no_newline"].(string)
	if strings.HasSuffix(noNewline, "\n") {
		t.Errorf("|- must not end in a newline: %q", noNewline)
	}
}

func TestParseYAMLListOfMaps(t *testing.T) {
	data := []byte(`
checks:
  - name: first
    command: make test
    expect_exit: 0
  - name: second
    command: ./lint.sh
`)
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	list, ok := m["checks"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("checks = %#v", m["checks"])
	}
	first := list[0].(map[string]any)
	if first["name"] != "first" || first["command"] != "make test" || first["expect_exit"] != int64(0) {
		t.Errorf("first check = %#v", first)
	}
	second := list[1].(map[string]any)
	if second["name"] != "second" {
		t.Errorf("second check = %#v", second)
	}
}

func TestParseYAMLExplicitErrors(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		contains string
	}{
		{"tabs", "a:\n\tb: 1\n", "tabs"},
		{"key without value", "a\n", "expected 'key: value'"},
		{"sequence root", "- one\n- two\n", "must be a map"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseYAML([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("an error was expected for %q", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, it was expected to contain %q", err, tc.contains)
			}
		})
	}
}

// TestLoadRepoExample validates that the example YAML is correct: if the
// documentation breaks, the test fails.
func TestLoadRepoExample(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "test-key")

	path := filepath.Join("..", "..", "configs", "agent.yaml.example")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("the example is not at %s: %v", path, err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the repository example does not load: %v", err)
	}
	if cfg.LLM.Model == "" || cfg.Agent.MaxRetries == 0 {
		t.Errorf("incomplete configuration: %+v", cfg)
	}
	if cfg.Anchor.Kind == "command" && cfg.Anchor.Command == "" {
		t.Error("anchor of kind command with no command")
	}
}

func TestLoadUseCases(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "test-key")

	for _, useCase := range []string{"1-development.yaml", "2-data.yaml", "3-automation.yaml"} {
		t.Run(useCase, func(t *testing.T) {
			path := filepath.Join("..", "..", "configs", "cases", useCase)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("%s is not there: %v", path, err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("the case %s does not load: %v", useCase, err)
			}
			// Every case must define the complete flow.
			if cfg.Prompts.Analyze.User == "" || cfg.Prompts.Plan.User == "" || cfg.Prompts.Execute.User == "" {
				t.Errorf("the case %s does not define the three prompt templates", useCase)
			}
			if cfg.Anchor.Kind == "none" {
				t.Errorf("the case %s validates nothing (anchor.kind=none)", useCase)
			}
			if cfg.FinalAction.Kind == "none" {
				t.Errorf("the case %s does not define a final action", useCase)
			}
		})
	}
}

func TestValidateDetectsErrors(t *testing.T) {
	cases := []struct {
		name     string
		modify   func(*Config)
		contains string
	}{
		{"unknown source", func(c *Config) { c.TaskSource.Kind = "telepathy" }, "unknown task_source.kind"},
		{"file without path", func(c *Config) { c.TaskSource.Kind = "file" }, "requires 'path'"},
		{"api without url", func(c *Config) { c.TaskSource.Kind = "api" }, "requires 'url'"},
		{"anchor command without command", func(c *Config) { c.Anchor.Kind = "command" }, "requires 'command'"},
		{"sandbox chroot without root", func(c *Config) { c.Sandbox.Kind = "chroot" }, "requires 'root'"},
		{"unknown provider", func(c *Config) { c.LLM.Provider = "wizard" }, "unknown llm.provider"},
		{"no key", func(c *Config) { c.LLM.APIKey = "" }, "the LLM key is missing"},
		{"final api without url", func(c *Config) { c.FinalAction.Kind = "api" }, "requires 'url'"},
		{"log level", func(c *Config) { c.Agent.LogLevel = "verbose" }, "unknown agent.log_level"},
		{"invalid user", func(c *Config) { c.Sandbox.User = "a:b:c" }, "uid:gid"},
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

func TestDecodeUnknownKey(t *testing.T) {
	m, err := ParseYAML([]byte("llm:\n  model: x\n  favorite_color: blue\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil {
		t.Fatal("an unknown key must be an error, not silence")
	}
	if !strings.Contains(err.Error(), "favorite_color") || !strings.Contains(err.Error(), "llm") {
		t.Errorf("the error must name the key and its block: %q", err)
	}
}

func TestDecodeDurations(t *testing.T) {
	m, err := ParseYAML([]byte(`
anchor:
  timeout: 45s
llm:
  timeout: 90
sandbox:
  timeout: 5m
`))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decode(m, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Anchor.Timeout != 45*time.Second {
		t.Errorf("anchor.timeout = %v", cfg.Anchor.Timeout)
	}
	if cfg.LLM.Timeout != 90*time.Second {
		t.Errorf("a number without a suffix must be seconds: %v", cfg.LLM.Timeout)
	}
	if cfg.Sandbox.Timeout != 5*time.Minute {
		t.Errorf("sandbox.timeout = %v", cfg.Sandbox.Timeout)
	}
}

func TestEnvironmentWinsOverYAML(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_MODEL", "model-from-environment")
	t.Setenv("STARLIGHT_AGENT_MAX_RETRIES", "7")
	t.Setenv("STARLIGHT_SANDBOX_ISOLATE_NETWORK", "true")
	t.Setenv("STARLIGHT_LLM_API_KEY", "key")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "model-from-environment" {
		t.Errorf("model = %q", cfg.LLM.Model)
	}
	if cfg.Agent.MaxRetries != 7 {
		t.Errorf("max_retries = %d", cfg.Agent.MaxRetries)
	}
	if !cfg.Sandbox.IsolateNetwork {
		t.Error("isolate_network should be true")
	}
}

func TestInvalidEnvironment(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_AGENT_MAX_RETRIES", "many")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err == nil {
		t.Fatal("an invalid integer in the environment must give an error")
	}
}

func TestParseUser(t *testing.T) {
	uid, gid, err := ParseUser("1000:1001")
	if err != nil || uid != 1000 || gid != 1001 {
		t.Errorf("1000:1001 -> %d %d %v", uid, gid, err)
	}
	uid, gid, err = ParseUser("1000")
	if err != nil || uid != 1000 || gid != 1000 {
		t.Errorf("1000 -> %d %d %v", uid, gid, err)
	}
	if _, _, err := ParseUser("thousand"); err == nil {
		t.Error("a non-numeric user must fail")
	}
}

func TestValidateAcceptsOllamaProvider(t *testing.T) {
	cfg := Default()
	cfg.LLM.Provider = "ollama"
	cfg.LLM.APIKey = "x"
	cfg.LLM.BaseURL = "https://ollama.com/v1"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ollama provider should be valid: %v", err)
	}
}

// TestOrListReadsLikeEnglish: the accepted values are listed the way they would be read
// aloud, because the message is shown to a person who has just mistyped a setting.
//
// The singular case is not decoration: "unknown agent.on_failure.kind: ... (use " is what
// an empty list produces, and a one-value list is what a setting with a single accepted
// spelling produces. Both have to read as something other than a dangling parenthesis.
func TestOrListReadsLikeEnglish(t *testing.T) {
	for _, tc := range []struct {
		items []string
		want  string
	}{
		{nil, "nothing"},
		{[]string{}, "nothing"},
		{[]string{"stdin"}, "stdin"},
		{[]string{"none", "command"}, "none or command"},
		{[]string{"none", "command", "api"}, "none, command or api"},
	} {
		if got := orList(tc.items); got != tc.want {
			t.Errorf("orList(%v) = %q, want %q", tc.items, got, tc.want)
		}
	}
}
