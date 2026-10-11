package tui

import (
	"strings"

	"github.com/madkoding/motita/internal/config"
)

// The first screen of each mode, and the screen of a search that matched nothing.

// noMatches is the filtered-to-empty state: what was searched, and the two keys that
// get the user back. It is deliberately not the same screen as "nothing asked yet" —
// one means "start", the other means "widen your search".
func (t *TUI) noMatches(inner int) []string {
	lines := []string{t.cell("", inner)}
	for _, l := range []string{
		t.trf("No line matches %q", t.query),
		"",
		t.tr("Ctrl+F  search again"),
		t.tr("Esc     show the whole conversation"),
	} {
		lines = append(lines, t.cell(t.muted(l), inner))
	}
	return append(lines, t.cell("", inner))
}

// emptyState is a designed first screen, not a blank one: what the view is for, what to type,
// how the two modes differ, the handful of commands a newcomer needs, and - when the setup is not
// usable yet - exactly how to fix it.
//
// Each block is short and wrapped to the width, so the screen reads the same on a netbook console
// as on a wide terminal. On a short terminal the layout keeps its TOP, which is why the most
// useful lines come first.
func (t *TUI) emptyState(inner int) []string {
	p := &emptyPage{t: t, inner: inner, width: inner - leftMargin}
	p.gap()

	if t.Notice != "" {
		p.text(t.Notice, colSuccess)
		p.gap()
	}

	p.screenIntro(t.screen)

	// A setup that cannot answer is said HERE, on the screen the user is looking at, with the
	// fix. A red dot in a corner is not an explanation.
	if llmCfg := t.Runner.Config().LLM; llmCfg.APIKey == "" && config.LLMNeedsKey(llmCfg) {
		p.gap()
		p.text(t.trf("%s No API key for %s yet: type /config to add one,",
			glyphMissing, providerName(llmCfg.Provider)), colWarning)
		p.text(t.trf("  or export %s before starting motita.", config.ProviderKeyVariable(llmCfg.Provider)), colWarning)
	}
	p.gap()
	return p.lines
}

// emptyPage collects the rows of the first screen, each one a cell of the given inner width
// with its text wrapped to width.
type emptyPage struct {
	t     *TUI
	inner int
	width int
	lines []string
}

// text adds s, wrapped, in the colour col. The static texts are translated here, once; a line
// built from parts is translated where it is built, as one format, so the catalog never holds a
// fragment.
func (p *emptyPage) text(s string, col int) {
	for _, l := range wordWrap(p.t.tr(s), p.width) {
		p.lines = append(p.lines, p.t.cell(p.t.color(col, 0, l), p.inner))
	}
}

// gap adds an empty row.
func (p *emptyPage) gap() { p.lines = append(p.lines, p.t.cell("", p.inner)) }

// pairs draws a short key/description table, with the keys in the accent colour and their
// descriptions aligned. A row that does not fit drops its description rather than wrapping
// in the middle of the table.
func (p *emptyPage) pairs(rows [][2]string) {
	t := p.t
	kw := 0
	for _, r := range rows {
		if n := visibleLen(r[0]); n > kw {
			kw = n
		}
	}
	for _, r := range rows {
		row := "  " + t.color(colAccent, 0, r[0]) + strings.Repeat(" ", kw-visibleLen(r[0])) + "  " + t.muted(t.tr(r[1]))
		if !t.fits(row, p.width) {
			row = "  " + t.color(colAccent, 0, r[0])
		}
		p.lines = append(p.lines, t.cell(row, p.inner))
	}
}

// examples draws what the user could type, after the mark their messages carry once sent.
func (p *emptyPage) examples(ex ...string) {
	for _, e := range ex {
		for _, l := range wordWrap(p.t.tr(e), p.width-4) {
			p.lines = append(p.lines, p.t.cell("  "+p.t.color(colAccent, 0, glyphUser)+" "+l, p.inner))
		}
	}
}

// screenIntro says what the screen is for and what to type in it.
func (p *emptyPage) screenIntro(screen Screen) {
	switch screen {
	case ScreenPlan:
		p.planIntro()
	case ScreenModels:
		p.text("Press Enter to ask the provider which models it offers.", colBase)
		p.text("It also checks that the endpoint and the key in your setup really work.", colMuted)
		p.gap()
		p.pairs([][2]string{{"/models <id>", "switch to that model for this session"}})
	case ScreenConfig:
		p.text("Press Enter to run the setup again: provider, sign-in, model and check.", colBase)
		p.text("Your current settings are kept as a backup, and the new ones apply right away.", colMuted)
	default:
		p.taskIntro()
	}
}

// planIntro is the first screen of Plan mode.
func (p *emptyPage) planIntro() {
	p.text("Plan mode: ask anything about your project.", colBase)
	p.text("motita reads files and runs read-only commands to answer. Nothing is changed.", colMuted)
	p.gap()
	p.text("Try something like", colMuted)
	p.examples("how is the configuration loaded?", "what would it take to add a --json flag?")
	p.gap()
	p.pairs([][2]string{{"Tab", "back to Task mode, to make changes"}})
}

// taskIntro is the first screen of Task mode.
func (p *emptyPage) taskIntro() {
	p.text("What should motita do? Describe the task and press Enter.", colBase)
	p.text("It makes the change, then runs your check to prove it worked before calling it done.", colMuted)
	p.gap()
	p.text("Try something like", colMuted)
	p.examples("add a --verbose flag and a test for it", "fix the failing test in the parser")
	p.gap()
	p.pairs([][2]string{
		{"Tab", "Plan mode: ask questions, nothing is changed"},
		{"/models", "choose another model"},
		{"/config", "change provider, key or check"},
		{"?", "every command and key"},
	})
}
