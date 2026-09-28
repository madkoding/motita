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
	// anchorAuto records the choice to let the PROJECT declare its own gate. It is
	// its own field rather than a sentinel inside anchorCommand, because a marker
	// travelling through a field that means "a command" is a marker every reader has
	// to know about — and the first reader that does not would write it into the file
	// as a command named "\x00auto".
	anchorAuto bool
	generated  time.Time
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

	b.WriteString("# motita configuration\n")
	b.WriteString("#\n")
	b.WriteString("# Written by `motita -init`")
	if !v.generated.IsZero() {
		b.WriteString(" on " + v.generated.UTC().Format("2006-01-02"))
	}
	b.WriteString(".\n")
	b.WriteString("#\n")
	b.WriteString("# Every value here can be overridden with an environment variable named\n")
	if p, ok := Lookup(v.provider); ok && p.Login != "" {
		b.WriteString("# MOTITA_<BLOCK>_<FIELD> (see README.md). No key is needed: the login\n")
		fmt.Fprintf(&b, "# comes from `%s`.\n", p.Login)
	} else {
		b.WriteString("# MOTITA_<BLOCK>_<FIELD> (see README.md), and the secret is NOT stored\n")
		fmt.Fprintf(&b, "# in this file: export %s instead.\n", keyVariableFor(v.provider))
	}
	b.WriteString("\n")

	b.WriteString("task_source:\n")
	b.WriteString("  kind: stdin          # take the task from what you type/pass in\n")
	b.WriteString("\n")

	// The anchor, and the three shapes it can take.
	//
	// `auto` is the recommended one because it is the only one that does not have to
	// be edited when the user moves to another project: the gate is read from
	// whatever directory the agent is standing in (see internal/anchor/detect.go).
	//
	// When the user picked "always pass while I try the agent out", the file records
	// `kind: none` and NOT a command that exits 0. The difference is not cosmetic:
	// `kind: none` refuses to declare PASS and says why, while `command: "true"`
	// PASSES on every run having checked nothing — so the agent reports "task
	// completed" over work it never did, and the guarantee that only the anchor can
	// declare PASS becomes a guarantee about nothing.
	b.WriteString("anchor:\n")
	switch {
	case v.anchorAuto:
		b.WriteString("  # The gate is read from the project the agent is working in: a\n")
		b.WriteString("  # Makefile's check/test, go test ./..., the lint/typecheck/test scripts\n")
		b.WriteString("  # of a package.json, cargo test. Nothing is hardcoded here, so this works\n")
		b.WriteString("  # unchanged on every project. A project with an unusual build names its\n")
		b.WriteString("  # own gate in .motita/anchor (one command per line).\n")
		b.WriteString("  kind: auto\n")
		b.WriteString("\n")
	case v.anchorCommand == "":
		b.WriteString("  # No check is configured, so NO task can be declared a success.\n")
		b.WriteString("  # Replace this with the command that decides the work is done, for example:\n")
		b.WriteString("  #   kind: command\n")
		b.WriteString("  #   command: make\n")
		b.WriteString("  #   args: [test]\n")
		b.WriteString("  # Or let the project declare it:\n")
		b.WriteString("  #   kind: auto\n")
		b.WriteString("  kind: none\n")
		b.WriteString("\n")
	default:
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
	}

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
	b.WriteString("  max_tokens: 16384\n")
	b.WriteString("  temperature: 0.2\n")
	b.WriteString("  timeout: 120s\n")
	b.WriteString("  max_attempts: 3\n")
	b.WriteString("\n")

	b.WriteString("agent:\n")
	b.WriteString("  max_retries: 3\n")
	b.WriteString("  workspace_dir: ./workspace\n")
	b.WriteString("  log_file: ./workspace/motita.log\n")
	b.WriteString("  log_level: info\n")
	b.WriteString("  log_console: true\n")

	return []byte(b.String())
}

// keyVariableFor returns the variable the generated header should tell the user
// about: the provider's own one when the catalogue defines it (OLLAMA_API_KEY for
// Ollama Cloud), and the generic name otherwise.
func keyVariableFor(providerID string) string {
	if p, ok := Lookup(providerID); ok && p.EnvKey != "" {
		return p.EnvKey
	}
	return "MOTITA_LLM_API_KEY"
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
