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
	colRed    = "\x1b[31m"
)

// The wizard's steps, in the order they are asked. They are named once so the breadcrumb under
// the banner and the "[2/5]" on every section cannot disagree about how many there are or what
// they are called.
var steps = []string{"Provider", "Connect", "Model", "Check", "Save"}

// The step numbers, for the callers.
const (
	stepProvider = iota + 1
	stepConnect
	stepModel
	stepCheck
	stepSave
)

// indent is the left margin of everything the wizard writes. One margin for the whole
// conversation is what makes it read as one screen instead of a log.
const indent = "  "

// printBanner writes the welcome: what is about to happen, how long it takes, and the two keys a
// newcomer needs (Enter takes the default, q leaves).
func printBanner(out io.Writer) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%s* motita%s %s· setup%s\n", indent, colBold, colReset, colDim, colReset)
	printDivider(out)
	fmt.Fprintf(out, "%sWelcome! Let's connect motita to an AI model. It takes about a minute.\n", indent)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%s\n", indent, breadcrumb())
	fmt.Fprintf(out, "%s%sPress Enter to accept the value in [brackets]. Type q to quit at any time.%s\n", indent, colGray, colReset)
}

// breadcrumb is the list of steps, so the user knows how far there is to go before the first
// question is even asked.
func breadcrumb() string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = fmt.Sprintf("%s%d%s %s", colCyan, i+1, colReset, s)
	}
	return strings.Join(parts, colGray+"  ›  "+colReset)
}

// printStep writes the header of one step: where the user is ("[2/5]") and the question the
// step answers.
func printStep(out io.Writer, n int, question string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%s[%d/%d] %s%s%s%s\n", indent, colCyan, n, len(steps), colReset, colBold, question, colReset)
}

// printSection writes a sub-heading inside a step: the login flows use it to name the account
// they are connecting to.
func printSection(out io.Writer, title string) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%s▎ %s%s%s\n", indent, colCyan, colBold, title, colReset)
}

// printOption writes one numbered choice of a menu: the number, the label padded to the menu's
// label column so every description starts in the same place, and the description.
func printOption(out io.Writer, n int, label string, width int, note string) {
	// The padding only exists to line the descriptions up: an option with none ends at its label.
	pad := ""
	if note != "" {
		pad = strings.Repeat(" ", width-len([]rune(label)))
		note = fmt.Sprintf("  %s%s%s", colDim, note, colReset)
	}
	fmt.Fprintf(out, "%s  %s%2d%s  %s%s%s%s%s\n", indent, colYellow, n, colReset, colBold, label, colReset, pad, note)
}

// labelWidth is the widest label of a menu, which is where its description column starts.
func labelWidth(labels []string) int {
	w := 0
	for _, l := range labels {
		if n := len([]rune(l)); n > w {
			w = n
		}
	}
	return w
}

// printProviders writes the provider menu: what each one is called and what it takes to connect.
func printProviders(out io.Writer, providers []Provider) {
	labels := make([]string, len(providers))
	for i, p := range providers {
		labels[i] = p.Short
	}
	w := labelWidth(labels)
	for i, p := range providers {
		printOption(out, i+1, p.Short, w, p.Access)
	}
}

// printModels writes the model menu. The first entry is the default, and it says so: the user
// who does not know which model to pick should not have to guess which one Enter takes.
func printModels(out io.Writer, models []Model) {
	labels := make([]string, len(models))
	for i, m := range models {
		labels[i] = m.ID
	}
	w := labelWidth(labels)
	for i, m := range models {
		note := m.Note
		if i == 0 {
			note = strings.TrimSpace("recommended · " + note)
			note = strings.TrimSuffix(note, " ·")
		}
		printOption(out, i+1, m.ID, w, note)
	}
}

// printSuccess writes a success line with a green checkmark.
func printSuccess(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s%s✓%s %s\n", indent, colGreen, colReset, fmt.Sprintf(format, args...))
}

// printWarning writes a problem the user can do something about, in yellow, with the "!" that
// keeps it readable without colour.
func printWarning(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s%s! %s%s\n", indent, colYellow, fmt.Sprintf(format, args...), colReset)
}

// printInfo writes an informational line in dim.
func printInfo(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s%s%s%s\n", indent, colDim, fmt.Sprintf(format, args...), colReset)
}

// printLink writes a URL in cyan, so the user can see it is clickable in terminals that support
// it.
func printLink(out io.Writer, url string) {
	fmt.Fprintf(out, "%s  %s%s%s\n", indent, colCyan, url, colReset)
}

// printCommand writes a command the user is meant to run, after a dim "$".
func printCommand(out io.Writer, command string) {
	fmt.Fprintf(out, "%s  %s$%s %s%s%s\n", indent, colGray, colReset, colBold, command, colReset)
}

// printPrompt writes the input prompt with a coloured arrow. The answer is typed on the same line.
func printPrompt(out io.Writer, prompt string) {
	fmt.Fprintf(out, "%s%s›%s %s ", indent, colCyan, colReset, prompt)
}

// printDivider writes a horizontal divider line.
func printDivider(out io.Writer) {
	fmt.Fprintf(out, "%s%s%s%s\n", indent, colGray, strings.Repeat("─", 60), colReset)
}

// printField writes one row of the review and of the summary: a dim label in a fixed column and
// its value.
func printField(out io.Writer, label, value string) {
	fmt.Fprintf(out, "%s  %s%-10s%s %s\n", indent, colDim, label, colReset, value)
}

// printSummary writes what was saved and what to do next. It is the last thing a first run shows
// before the interface opens, so it answers the only two questions left: did it work, and what now.
func printSummary(out io.Writer, res Result) {
	fmt.Fprintln(out)
	printDivider(out)
	printSuccess(out, "%sAll set!%s motita is connected to %s.", colBold, colReset, res.Provider.Short)
	fmt.Fprintln(out)
	printField(out, "Settings", res.ConfigPath)
	if res.CredentialsPath != "" {
		printField(out, "API key", res.CredentialsPath+colDim+"  (read automatically, "+credentialsProtection()+")"+colReset)
	}
	printField(out, "Model", res.Model)

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%sBefore your first task%s\n", indent, colBold, colReset)
	switch {
	case res.Provider.Login != "":
		printInfo(out, "Install it (%s), then log in:", res.Provider.ConsoleURL)
		printCommand(out, res.Provider.Login)
	case res.LoggedIn:
		printInfo(out, "Nothing: the login is stored and renewed automatically.")
	case res.Keyless:
		printInfo(out, "Make sure the Ollama server is running and the model is downloaded:")
		printCommand(out, "ollama pull "+res.Model)
	case res.CredentialsPath != "":
		printInfo(out, "Nothing: the key is saved and motita reads it on its own.")
	default:
		printWarning(out, "No API key yet. Add it by running the setup again (/config inside motita),")
		printInfo(out, "  or export it in your shell:")
		printCommand(out, "export "+res.Provider.EnvKey+"=...")
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s%sStart working%s\n", indent, colBold, colReset)
	printInfo(out, "Open a terminal in your project and run:")
	printCommand(out, "motita")
	printInfo(out, "Change any of this later with /config inside motita, or with: motita -init")
	printDivider(out)
}

// maskKey shows enough of a key to recognise it and not enough to use it: the review prints it,
// and a screen gets shared.
func maskKey(key string) string {
	r := []rune(key)
	if len(r) <= 10 {
		return strings.Repeat("•", 6)
	}
	return string(r[:3]) + "…" + string(r[len(r)-4:])
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
			for i < len(s) {
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
