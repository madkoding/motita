package tui

import (
	"fmt"
	"strings"

	"github.com/madkoding/starlight/internal/config"
)

const (
	bannerWidth   = 60
	maxScrollback = 250
)

// ASCII art banner for Starlight plus a star.
var bannerLines = []string{
	"______           ___      __   __ ",
	"  / __/ /____ _____/ (_)__ _/ /  / /_",
	" _\\ \\// __/  `_ / __/ / /  `_ / _ \\| __/",
	"/___/\\__/\\_,_/_/ /_/_/\\_, /_//_/\\__/ ",
	"                     /___/            ",
	"",
	"          *  autonomous agent  *               ",
}

// drawFrame paints the complete frame: banner, status bar, chat bubbles, footer.
func (t *TUI) drawFrame() {
	var b strings.Builder
	// Clear screen and move cursor to top-left; hide cursor during redraw.
	b.WriteString("\x1b[2J\x1b[H")
	b.WriteString("\x1b[?25l")

	cfg := t.Runner.Config()
	b.WriteString(t.banner())
	b.WriteString(t.statusBar(cfg))
	b.WriteString(t.messagesArea())
	b.WriteString(t.footer())

	// Show cursor at the input prompt.
	b.WriteString("\x1b[?25h")
	fmt.Fprint(t.Out, b.String())
}

func (t *TUI) banner() string {
	var b strings.Builder
	for _, line := range bannerLines {
		b.WriteString(t.color(6, 0, "  "+line))
		b.WriteString("\n")
	}
	return b.String()
}

func (t *TUI) statusBar(cfg config.Config) string {
	provider := cfg.LLM.Provider
	if provider == "" {
		provider = "openai"
	}
	model := cfg.LLM.Model
	if model == "" {
		model = "unknown"
	}
	keyStatus := "missing"
	keyDot := t.color(1, 0, "\u25cf")
	if cfg.LLM.APIKey != "" {
		keyStatus = "present"
		keyDot = t.color(2, 0, "\u25cf")
	}
	reasoning := "off"
	if cfg.LLM.Reasoning.Enabled {
		reasoning = cfg.LLM.Reasoning.Level
	}
	line := fmt.Sprintf("  %s \u2502 model:%s \u2502 key:%s %s \u2502 reasoning:%s",
		t.color(7, 0, "provider:"+provider),
		t.color(7, 0, model),
		t.color(7, 0, keyStatus),
		keyDot,
		t.color(7, 0, reasoning))
	return line + "\n"
}

// messagesArea renders the conversation as scrollable chat bubbles.
func (t *TUI) messagesArea() string {
	var b strings.Builder
	msgs := t.visibleMessages()
	for _, m := range msgs {
		b.WriteString(t.renderBubble(m))
	}
	if len(t.messages) > len(msgs) {
		b.WriteString(t.color(8, 0, fmt.Sprintf("  ... %d older messages\n", len(t.messages)-len(msgs))))
	}
	return b.String()
}

const bubbleWidth = 68

func (t *TUI) renderBubble(m Message) string {
	var b strings.Builder
	width := bubbleWidth - 6
	switch m.Author {
	case AuthorUser:
		b.WriteString(t.color(6, 0, "  \u250c\u2500 you \u2500"+strings.Repeat("\u2500", width-4)+"\u2510\n"))
		for _, line := range wordWrap(m.Text, width) {
			b.WriteString(fmt.Sprintf("  \u2502 %s%s \u2502\n", line, strings.Repeat(" ", width-len(line))))
		}
		b.WriteString(t.color(6, 0, "  \u2514"+strings.Repeat("\u2500", width+2)+"\u2518\n"))
	case AuthorAgent:
		text := m.Text
		if m.Pending {
			text = t.color(3, 0, text)
		}
		b.WriteString(t.color(2, 0, "  \u250c\u2500 starlight \u2500"+strings.Repeat("\u2500", width-10)+"\u2510\n"))
		for _, line := range wordWrap(text, width) {
			b.WriteString(fmt.Sprintf("  \u2502 %s%s \u2502\n", line, strings.Repeat(" ", width-len(line))))
		}
		b.WriteString(t.color(2, 0, "  \u2514"+strings.Repeat("\u2500", width+2)+"\u2518\n"))
	case AuthorSystem:
		b.WriteString(t.color(8, 0, fmt.Sprintf("  \u2500\u2500 %s \u2500\u2500\n", m.Text)))
	}
	return b.String()
}

func (t *TUI) visibleMessages() []Message {
	if len(t.messages) <= maxScrollback {
		return t.messages
	}
	return t.messages[len(t.messages)-maxScrollback:]
}

// footer: mode tabs + key hints + input prompt.
func (t *TUI) footer() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(t.tabsLine())
	b.WriteString(t.keyHints())
	b.WriteString(t.inputPrompt())
	return b.String()
}

func (t *TUI) tabsLine() string {
	var parts []string
	for _, s := range screenOrder {
		label := s.String()
		if t.screen == s {
			parts = append(parts, t.color(0, 6, " "+label+" "))
		} else {
			parts = append(parts, t.color(8, 0, " "+label+" "))
		}
	}
	return "  " + strings.Join(parts, " ") + "\n"
}

func (t *TUI) keyHints() string {
	hints := []string{
		"Tab next",
		"/t task",
		"/p plan",
		"/m models",
		"/c config",
		"/r reasoning",
		"q quit",
	}
	var colored []string
	for _, h := range hints {
		parts := strings.SplitN(h, " ", 2)
		colored = append(colored, t.color(6, 0, parts[0])+" "+t.color(8, 0, parts[1]))
	}
	return "  " + strings.Join(colored, "  ") + "\n"
}

func (t *TUI) inputPrompt() string {
	return t.color(7, 0, fmt.Sprintf("  %s > ", t.screen.String()))
}

// wordWrap splits s into lines of at most width bytes without breaking words when possible.
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		var cur strings.Builder
		for _, word := range strings.Fields(para) {
			if cur.Len()+len(word)+1 > width {
				if cur.Len() == 0 {
					lines = append(lines, word[:width])
					word = word[width:]
					for len(word) > width {
						lines = append(lines, word[:width])
						word = word[width:]
					}
					cur.WriteString(word)
				} else {
					lines = append(lines, strings.TrimSpace(cur.String()))
					cur.Reset()
					cur.WriteString(word)
				}
			} else {
				if cur.Len() > 0 {
					cur.WriteByte(' ')
				}
				cur.WriteString(word)
			}
		}
		if cur.Len() > 0 {
			lines = append(lines, strings.TrimSpace(cur.String()))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// color returns an ANSI-coloured string. fg/bg use the 16-colour palette.
func (t *TUI) color(fg, bg int, s string) string {
	if t.NoColor {
		return s
	}
	if bg == 0 {
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", 30+fg, s)
	}
	return fmt.Sprintf("\x1b[%d;%dm%s\x1b[0m", 30+fg, 40+bg, s)
}
