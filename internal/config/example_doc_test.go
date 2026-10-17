package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// configs/agent.yaml.example is the file an operator copies. What it says about the defaults has
// to be what Default() does, or the operator builds the wrong model of the agent from it.
func TestTheExampleConfigTellsTheTruthAboutTheDefaults(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "test-key")
	path := filepath.Join("..", "..", "configs", "agent.yaml.example")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the example: %v", err)
	}
	text := string(raw)
	lines := strings.Split(text, "\n")

	// The gateway binds loopback by default, and gateway.tls is a real block.
	for _, stale := range []string{"LISTENS EVERYWHERE", "NO TLS"} {
		if strings.Contains(text, stale) {
			t.Errorf("the example says %q, but the gateway binds %s by default and has gateway.tls",
				stale, defaultGatewayListen)
		}
	}
	_, port, _ := strings.Cut(defaultGatewayListen, ":")
	_, gateway, _ := strings.Cut(text, "# Gateway:")
	gateway, _, _ = strings.Cut(gateway, "\nschedule:")
	if !strings.Contains(gateway, "127.0.0.1:"+port) {
		t.Error("the gateway section of the example no longer names the default address; change this test with it")
	}
	for _, m := range regexp.MustCompile(`127\.0\.0\.1:(\d+)`).FindAllStringSubmatch(gateway, -1) {
		if m[1] != port {
			t.Errorf("the example points at 127.0.0.1:%s, but the gateway listens on port %s", m[1], port)
		}
	}

	// A value shown that is NOT the default carries a "default:" note on its line.
	example, err := Load(path)
	if err != nil {
		t.Fatalf("load the example: %v", err)
	}
	def := Default()
	keys := []struct {
		path       []string
		shown, def any
	}{
		{[]string{"anchor", "kind"}, example.Anchor.Kind, def.Anchor.Kind},
		{[]string{"sandbox", "kind"}, example.Sandbox.Kind, def.Sandbox.Kind},
		{[]string{"final_action", "kind"}, example.FinalAction.Kind, def.FinalAction.Kind},
		{[]string{"agent", "max_retries"}, example.Agent.MaxRetries, def.Agent.MaxRetries},
		{[]string{"agent", "workspace_dir"}, example.Agent.WorkspaceDir, def.Agent.WorkspaceDir},
		{[]string{"agent", "log_file"}, example.Agent.LogFile, def.Agent.LogFile},
		{[]string{"agent", "on_failure", "kind"}, example.Agent.OnFailure.Kind, def.Agent.OnFailure.Kind},
	}
	for _, k := range keys {
		line := exampleLine(lines, k.path...)
		if line == "" {
			t.Errorf("the example has no %s line; change this test with it", strings.Join(k.path, "."))
			continue
		}
		if fmt.Sprint(k.shown) != fmt.Sprint(k.def) && !strings.Contains(line, "default:") {
			t.Errorf("the example shows %s = %v, but the default is %v, and the line does not say so:\n%s",
				strings.Join(k.path, "."), k.shown, k.def, line)
		}
	}

	// The provider comment names every provider the validation accepts.
	provider := exampleLine(lines, "llm", "provider")
	for _, p := range Providers {
		if !regexp.MustCompile(`[\s|]` + regexp.QuoteMeta(p) + `(\s|$)`).MatchString(provider) {
			t.Errorf("the llm.provider comment does not list %q:\n%s", p, provider)
		}
	}
}

// The binary is called motita. A copied command that names anything else fails with
// "command not found".
func TestTheExampleConfigsNameTheRealBinary(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "configs", "*", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "configs", "agent.yaml.example"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(raw), "motita-agent") {
			t.Errorf("%s names a motita-agent binary; the build produces motita", filepath.Base(f))
		}
	}
}

// exampleLine returns the line of a nested key (for example "agent", "on_failure", "kind"), or ""
// when the path is not there.
func exampleLine(lines []string, path ...string) string {
	depth, i := -1, 0
	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(l) - len(trimmed)
		if depth >= 0 && indent <= depth {
			return ""
		}
		if strings.HasPrefix(trimmed, path[i]+":") {
			if i == len(path)-1 {
				return l
			}
			depth = indent
			i++
		}
	}
	return ""
}
