package tui

import (
	"fmt"
	"strings"

	"github.com/madkoding/starlight/internal/config"
)

// drawFrame paints the whole screen: status bar, tabs, messages and input prompt.
func (t *TUI) drawFrame() {
	var b strings.Builder

	// Clear screen and move cursor to top-left.
	b.WriteString("\x1b[2J\x1b[H")

	cfg := t.Runner.Config()
	b.WriteString(t.statusBar(cfg))
	b.WriteString(t.tabsLine())
	b.WriteString(t.messagesArea())
	b.WriteString(t.inputPrompt())

	fmt.Fprint(t.Out, b.String())
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
	return fmt.Sprintf("%s\n%s\n", t.color(4, 0, " Starlight"), line)
}

func (t *TUI) tabsLine() string {
	var parts []string
	for _, s := range screenOrder {
		label := s.String()
		if t.screen == s {
			parts = append(parts, t.color(0, 7, " "+label+" "))
		} else {
			parts = append(parts, t.color(7, 0, " "+label+" "))
		}
	}
	return strings.Join(parts, "") + "\n" + t.color(7, 0, strings.Repeat("─", 60)) + "\n"
}

func (t *TUI) messagesArea() string {
	var b strings.Builder
	for _, m := range t.messages {
		prefix := t.colorPrefix(m.Author)
		text := m.Text
		if m.Pending {
			text = t.color(3, 0, text) // yellow pending text
		}
		b.WriteString(fmt.Sprintf("%s %s\n", prefix, text))
	}
	return b.String()
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
