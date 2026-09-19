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

// drawFrame paints the whole screen: banner, status bar, tabs, scrollable messages and input prompt.
func (t *TUI) drawFrame() {
	var b strings.Builder

	// Clear screen and move cursor to top-left.
	b.WriteString("\x1b[2J\x1b[H")
	// Hide cursor while we redraw; show it at the prompt.
	b.WriteString("\x1b[?25l")

	cfg := t.Runner.Config()
	b.WriteString(t.banner())
	b.WriteString(t.statusBar(cfg))
	b.WriteString(t.tabsLine())
	b.WriteString(t.messagesArea())
	b.WriteString(t.inputPrompt())
	// Show cursor at the end of the prompt.
	b.WriteString("\x1b[?25h")

	fmt.Fprint(t.Out, b.String())
}

func (t *TUI) banner() string {
	if t.NoColor {
		return strings.Join(bannerLines, "\n") + "\n"
	}
	var b strings.Builder
	for _, line := range bannerLines {
		b.WriteString(t.color(6, 0, line))
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
	keyColor := 1 // red
	if cfg.LLM.APIKey != "" {
		keyStatus = "present"
		keyColor = 2 // green
	}

	reasoning := "off"
	if cfg.LLM.Reasoning.Enabled {
		reasoning = cfg.LLM.Reasoning.Level
	}

	parts := []string{
		t.color(7, 0, fmt.Sprintf("provider:%s", provider)),
		t.color(7, 0, fmt.Sprintf("model:%s", model)),
		t.color(keyColor, 0, fmt.Sprintf("key:%s", keyStatus)),
		t.color(7, 0, fmt.Sprintf("reasoning:%s", reasoning)),
	}

	line := strings.Join(parts, " │ ")
	return fmt.Sprintf("%s\n", line)
}

func (t *TUI) tabsLine() string {
	var parts []string
	for _, s := range screenOrder {
		label := s.String()
		if t.screen == s {
			parts = append(parts, t.color(0, 6, " "+label+" "))
		} else {
			parts = append(parts, t.color(7, 0, " "+label+" "))
		}
	}
	return strings.Join(parts, "") + "\n" + t.color(7, 0, strings.Repeat("─", bannerWidth)) + "\n"
}

// messagesArea renders a scrollable view of the conversation. It keeps only the
// last maxScrollback messages and fits the visible output to the terminal height when
// a height hint is available.
func (t *TUI) messagesArea() string {
	var b strings.Builder
	msgs := t.visibleMessages()
	for _, m := range msgs {
		prefix := t.colorPrefix(m.Author)
		text := m.Text
		if m.Pending {
			text = t.color(3, 0, text) // yellow pending text
		}
		// Word-wrap long lines to bannerWidth so the chat remains readable on narrow terminals.
		wrapped := wordWrap(text, bannerWidth-2)
		for i, line := range wrapped {
			indent := ""
			if i > 0 {
				indent = strings.Repeat(" ", 6)
			}
			b.WriteString(fmt.Sprintf("%s %s%s\n", prefix, indent, line))
			prefix = "  " // only first line gets the avatar
		}
	}
	if len(t.messages) > len(msgs) {
		b.WriteString(t.color(7, 0, fmt.Sprintf("  ... %d older messages\n", len(t.messages)-len(msgs))))
	}
	return b.String()
}

// visibleMessages returns the tail of the conversation that fits on screen.
func (t *TUI) visibleMessages() []Message {
	if len(t.messages) <= maxScrollback {
		return t.messages
	}
	return t.messages[len(t.messages)-maxScrollback:]
}

func (t *TUI) colorPrefix(a Author) string {
	switch a {
	case AuthorUser:
		return t.color(6, 0, " you")
	case AuthorAgent:
		return t.color(2, 0, " ⭐")
	case AuthorSystem:
		return t.color(3, 0, " ⚙")
	}
	return t.color(7, 0, " ?")
}

func (t *TUI) inputPrompt() string {
	return fmt.Sprintf("\n%s ", t.color(7, 0, t.screen.String()+">"))
}

// wordWrap splits s into lines of at most width runes without breaking words when possible.
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
					// word itself is too long: hard split
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

// color returns an ANSI-coloured string. fg/bg use the 16-colour palette
// (0-15). A NoColor TUI returns the string unchanged.
func (t *TUI) color(fg, bg int, s string) string {
	if t.NoColor {
		return s
	}
	if bg == 0 {
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", 30+fg, s)
	}
	return fmt.Sprintf("\x1b[%d;%dm%s\x1b[0m", 30+fg, 40+bg, s)
}
