package onboard

import (
	"fmt"
	"io"
	"strings"
)

// ANSI colour codes used by the wizard. They are written unconditionally: the
// caller (app.go) already checks NO_COLOR and TERM=dumb before deciding whether
// to pass a writer that can interpret them, and a pipe or a test buffer strips
// them or ignores them. The wizard itself never decides whether to emit colour
// — that would duplicate a decision the caller already owns.
const (
	colReset  = "\x1b[0m"
	colBold   = "\x1b[1m"
	colDim    = "\x1b[2m"
	colCyan   = "\x1b[36m"
	colGreen  = "\x1b[32m"
	colYellow = "\x1b[33m"
	colGray   = "\x1b[90m"
)

// printBanner writes the welcome header to the output.
func printBanner(out io.Writer) {
	fmt.Fprintf(out, "%s  motita — your autonomous coding agent%s\n", colBold, colReset)
	fmt.Fprintf(out, "%s  Let's get you set up. This takes about a minute.%s\n", colDim, colReset)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s  ─────────────────────────────────────────────────────────%s\n", colGray, colReset)
	fmt.Fprintln(out)
}

// printSection writes a section header with a coloured prefix.
func printSection(out io.Writer, title string) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s▎ %s%s%s\n", colCyan, colBold, title, colReset)
}

// printProvider writes a provider entry in the menu, with its name in bold and
// the description in dim.
func printProvider(out io.Writer, n int, p Provider) {
	fmt.Fprintf(out, "  %s%d.%s %s%s%s %s— %s%s\n",
		colYellow, n, colReset,
		colBold, p.Name, colReset,
		colDim, p.ID, colReset)
}

// printModel writes a model entry in the menu, with its label in bold and the
// note in dim.
func printModel(out io.Writer, n int, m Model) {
	note := ""
	if m.Note != "" {
		note = fmt.Sprintf(" %s— %s%s", colDim, m.Note, colReset)
	}
	fmt.Fprintf(out, "  %s%d.%s %s%s%s%s\n",
		colYellow, n, colReset,
		colBold, m.Label, colReset,
		note)
}

// printSuccess writes a success line with a green checkmark.
func printSuccess(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s✓%s %s\n", colGreen, colReset, fmt.Sprintf(format, args...))
}

// printInfo writes an informational line in dim.
func printInfo(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s%s%s\n", colDim, fmt.Sprintf(format, args...), colReset)
}

// printLink writes a URL in cyan and underlined, so the user can see it is
// clickable in terminals that support it.
func printLink(out io.Writer, url string) {
	fmt.Fprintf(out, "  %s%s%s\n", colCyan, url, colReset)
}

// printPrompt writes the input prompt with a coloured arrow.
func printPrompt(out io.Writer, prompt string) {
	fmt.Fprintf(out, "%s▸%s %s ", colCyan, colReset, prompt)
}

// printCancelHint writes the hint about pressing q to cancel.
func printCancelHint(out io.Writer) {
	fmt.Fprintf(out, "%s  (press q to cancel at any time)%s\n", colGray, colReset)
}

// printDivider writes a horizontal divider line.
func printDivider(out io.Writer) {
	fmt.Fprintf(out, "%s  ─────────────────────────────────────────────────────────%s\n", colGray, colReset)
}

// printSummary writes the final summary with colour-coded sections.
func printSummary(out io.Writer, res Result) {
	fmt.Fprintln(out)
	printDivider(out)
	fmt.Fprintln(out)
	printSuccess(out, "Configuration written to %s", res.ConfigPath)
	fmt.Fprintf(out, "  %sprovider:%s %s\n", colDim, colReset, res.Provider)
	fmt.Fprintf(out, "  %smodel:%s    %s\n", colDim, colReset, res.Model)

	if res.CredentialsPath != "" {
		fmt.Fprintln(out)
		printSuccess(out, "Credentials written to %s", res.CredentialsPath)
		printInfo(out, "%s", credentialsProtection())
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%sNext steps:%s\n", colBold, colReset)
	if res.CredentialsPath != "" {
		fmt.Fprintf(out, "  %s$%s source %s\n", colGray, colReset, res.CredentialsPath)
	} else {
		fmt.Fprintf(out, "  %s$%s export %s=...\n", colGray, colReset, res.Provider.EnvKey)
	}
	fmt.Fprintf(out, "  %s$%s ./motita -config %s -validate-config\n", colGray, colReset, res.ConfigPath)
	fmt.Fprintf(out, "  %s$%s ./motita -config %s -task \"what to do\"\n", colGray, colReset, res.ConfigPath)
	fmt.Fprintln(out)
	printDivider(out)
}

// stripANSI removes ANSI escape sequences from a string. It is used by tests
// that need to compare the logical content of the wizard output without the
// colour codes.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip the escape sequence: ESC [ ... letter
			i++
			for i < len(s) && i < len(s)+20 {
				if s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' {
					i++
					break
				}
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
