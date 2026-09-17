// Package template substitutes {{name}} variables in prompts.
//
// It is deliberately minimal: no logic, no conditionals and no evaluation. A
// prompt is text with holes, and this only fills the holes with what the agent
// knows at that moment. That way the reasoning engine never invents context:
// either the variable exists and is substituted, or a warning is issued.
package template

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// Render fills in the variables. Unknown ones are left as they are and returned
// in missing, so the caller can log them instead of handing the LLM a prompt
// with silent holes.
func Render(text string, variables map[string]string) (string, []string) {
	missing := map[string]bool{}

	out := placeholder.ReplaceAllStringFunc(text, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		if value, ok := variables[name]; ok {
			return value
		}
		missing[name] = true
		return m
	})

	if len(missing) == 0 {
		return out, nil
	}
	list := make([]string, 0, len(missing))
	for name := range missing {
		list = append(list, name)
	}
	sort.Strings(list)
	return out, list
}

// Variables lists the variables a text uses (useful to document the available
// prompts and to validate the configuration at start-up).
func Variables(text string) []string {
	seen := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
	}
	list := make([]string, 0, len(seen))
	for name := range seen {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// History concatenates a history of failed attempts for the prompt.
func History(attempts []string) string {
	if len(attempts) == 0 {
		return "## PREVIOUS ATTEMPTS\n(none: this is the first attempt)"
	}
	var sb strings.Builder
	sb.WriteString("## PREVIOUS ATTEMPTS THAT FAILED\n")
	for i, attempt := range attempts {
		fmt.Fprintf(&sb, "### Failed attempt %d\n%s\n", i+1, attempt)
	}
	return sb.String()
}
