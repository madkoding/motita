package onboard

import (
	"fmt"
	"strings"
	"time"
)

// configValues is what goes into the generated file.
type configValues struct {
	provider      string
	model         string
	baseURL       string
	anchorCommand string
	anchorArgs    []string
	generated     time.Time
}

// renderConfig builds a configuration that works on its own, with no reference to
// the repository: the task comes from standard input, the check is the one the
// user chose and the working directory is relative to wherever the agent runs.
//
// The shape (block names, nesting) is the one internal/config parses; a generated
// file that the program cannot load would be worse than no wizard at all, so this
// is exercised against the real loader in the tests.
func renderConfig(v configValues) []byte {
	var b strings.Builder

	b.WriteString("# starlight-agent configuration\n")
	b.WriteString("#\n")
	b.WriteString("# Written by `starlight-agent -init`")
	if !v.generated.IsZero() {
		b.WriteString(" on " + v.generated.UTC().Format("2006-01-02"))
	}
	b.WriteString(".\n")
	b.WriteString("#\n")
	b.WriteString("# Every value here can be overridden with an environment variable named\n")
	b.WriteString("# STARLIGHT_<BLOCK>_<FIELD> (see README.md), and the secret is NOT stored\n")
	b.WriteString("# in this file: export STARLIGHT_LLM_API_KEY (or OPENAI_API_KEY) instead.\n")
	b.WriteString("\n")

	b.WriteString("task_source:\n")
	b.WriteString("  kind: stdin          # take the task from what you type/pass in\n")
	b.WriteString("\n")

	b.WriteString("anchor:\n")
	b.WriteString("  kind: command\n")
	fmt.Fprintf(&b, "  command: %s\n", yamlScalar(v.anchorCommand))
	if len(v.anchorArgs) > 0 {
		b.WriteString("  args: [")
		for i, a := range v.anchorArgs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(yamlScalar(a))
		}
		b.WriteString("]\n")
	}
	b.WriteString("  expect_exit: 0\n")
	b.WriteString("\n")

	b.WriteString("sandbox:\n")
	b.WriteString("  kind: none           # none | chroot | cgroups; none works everywhere\n")
	b.WriteString("  memory_mb: 512\n")
	b.WriteString("  cpu_seconds: 30\n")
	b.WriteString("  timeout: 60s\n")
	b.WriteString("\n")

	b.WriteString("llm:\n")
	fmt.Fprintf(&b, "  provider: %s\n", yamlScalar(v.provider))
	fmt.Fprintf(&b, "  model: %s\n", yamlScalar(v.model))
	fmt.Fprintf(&b, "  base_url: %s\n", yamlScalar(v.baseURL))
	b.WriteString("  max_tokens: 2048\n")
	b.WriteString("  temperature: 0.2\n")
	b.WriteString("  timeout: 120s\n")
	b.WriteString("  max_attempts: 3\n")
	b.WriteString("\n")

	b.WriteString("agent:\n")
	b.WriteString("  max_retries: 3\n")
	b.WriteString("  workspace_dir: ./workspace\n")
	b.WriteString("  log_file: ./workspace/starlight.log\n")
	b.WriteString("  log_level: info\n")
	b.WriteString("  log_console: true\n")

	return []byte(b.String())
}

// yamlScalar quotes a value only when it needs it, so the generated file stays
// readable while never producing invalid YAML.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	needsQuotes := strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`,") ||
		strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") ||
		strings.Contains(s, "\n") || strings.Contains(s, "\t")
	if !needsQuotes {
		switch strings.ToLower(s) {
		case "true", "false", "null", "yes", "no", "on", "off", "~":
			needsQuotes = true
		}
	}
	if !needsQuotes {
		return s
	}
	// Inside a double-quoted YAML scalar the escapes are the ones below: a raw
	// newline would be folded into a space by the parser, silently changing the
	// value, and a raw tab is not allowed there at all.
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
		"\r", `\r`,
	).Replace(s)
	return `"` + escaped + `"`
}
