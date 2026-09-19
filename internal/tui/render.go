package tui

import (
	"fmt"
	"strings"

	"github.com/madkoding/starlight/internal/config"
)

const (
	maxScrollback = 300
)

// ASCII art banner for Starlight.
var bannerLines = []string{
	"\x1b[0;97m\u2580\u2580\u2580\u2580\x1b[0;37m\u2580\u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580 \x1b[0;97m\u2584\x1b[0;97;47m\u2593\u2592\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m      \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\x1b[0;90;47m\u2591\u2592\x1b[0;37m \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m  \u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580\x1b[0m",
	"\x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2580\u2580\u2580 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u2588\u2588\u2588\u2588\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u258c\x1b[0;37m\u2580\u2580\u2580 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0m",
	"\x1b[0;37m \u2580\u2580\u2580\u2580\u2588\u2588\u2584 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\u2584\u2580\u2580\u2580  \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\x1b[0;90m\u2580\u2580\u2580\u2580\x1b[0;37m \x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588 \u2584\u2584\u2584\u2584 \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0m",
	"\x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;97;47m\u2591\x1b[0;37m\u2588\x1b[0;93;47m\u2591\x1b[0;37m  \u2588\u2588\u2584 \x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588\x1b[0;90m\u2590\u258c\x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588 \x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588  \x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0m",
	"\x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588\u2584\u2584\u2588\x1b[0;93;47m\u2591\x1b[0;92m\u2580\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;93;47m\u2591\u2592\u2593\x1b[0;37m \x1b[0;90m\u2592\x1b[0;37m \u2588\u2588\u2588  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \u2580\u2588\u2588\u2584\u2584\u2588\u2588\u2588 \u2588\u2588\u2588 \u2580\u2588\u2588\u2584\u2584\u2588\u2588\u2588 \u2588\u2588\u2588  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;93;47m\u2591\u2592\u2593\x1b[0;37m \x1b[0;90m\u2592\x1b[0m",
}

const frameWidth = 72

// drawFrame paints the complete frame: header box, status bar, chat bubbles, footer.
func (t *TUI) drawFrame() {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	b.WriteString("\x1b[?25l")

	cfg := t.Runner.Config()
	b.WriteString(t.headerBox())
	b.WriteString(t.statusBar(cfg))
	b.WriteString(t.messagesArea())
	b.WriteString(t.footer())

	b.WriteString("\x1b[?25h")
	fmt.Fprint(t.Out, b.String())
}

// headerBox renders the title inside a thick double-line frame.
func (t *TUI) headerBox() string {
	var b strings.Builder
	inner := frameWidth - 4

	b.WriteString(t.color(6, 0, "╔"))
	b.WriteString(t.color(6, 0, strings.Repeat("\u2550", inner)))
	b.WriteString(t.color(6, 0, "\u2557\n"))

	for _, line := range bannerLines {
		pad := inner - visibleLen(line)
		if pad < 0 {
			pad = 0
		}
		leftPad := pad / 2
		rightPad := pad - leftPad
		b.WriteString(t.color(6, 0, "\u2551"))
		b.WriteString(strings.Repeat(" ", leftPad))
		b.WriteString(t.color(7, 0, line))
		b.WriteString(strings.Repeat(" ", rightPad))
		b.WriteString(t.color(6, 0, "\u2551\n"))
	}

	b.WriteString(t.color(6, 0, "\u255A"))
	b.WriteString(t.color(6, 0, strings.Repeat("\u2550", inner)))
	b.WriteString(t.color(6, 0, "\u255D\n"))
	return b.String()
}

// visibleLen returns the displayed width of an ASCII/Unicode string.
func visibleLen(s string) int {
	return len([]rune(s))
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
	line := fmt.Sprintf("\u2502 %s \u2502 %s \u2502 key:%s %s \u2502 %s \u2502",
		t.color(7, 0, "provider:"+provider),
		t.color(7, 0, "model:"+model),
		t.color(7, 0, keyStatus),
		keyDot,
		t.color(7, 0, "reasoning:"+reasoning))
	pad := frameWidth - visibleLen(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad-1) + "\u2502"
	}
	return " " + t.color(6, 0, "\u250C") + t.color(6, 0, strings.Repeat("\u2500", frameWidth-2)) + t.color(6, 0, "\u2510\n") +
		" " + t.color(6, 0, line) + "\n" +
		" " + t.color(6, 0, "\u2514") + t.color(6, 0, strings.Repeat("\u2500", frameWidth-2)) + t.color(6, 0, "\u2518\n")
}

// messagesArea renders the conversation as scrollable chat bubbles.
func (t *TUI) messagesArea() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(t.color(6, 0, "  \u250C"+strings.Repeat("\u2500", frameWidth-6)+"\u2510\n"))
	b.WriteString(t.color(6, 0, "  \u2502"+strings.Repeat(" ", frameWidth-6)+"\u2502\n"))

	msgs := t.visibleMessages()
	for _, m := range msgs {
		b.WriteString(t.renderBubble(m))
	}
	if len(t.messages) > len(msgs) {
		b.WriteString(t.color(8, 0, fmt.Sprintf("  \u2502 ... %d older messages%s\u2502\n", len(t.messages)-len(msgs), strings.Repeat(" ", frameWidth-24-len(fmt.Sprintf("%d", len(t.messages)-len(msgs)))))))
	}

	b.WriteString(t.color(6, 0, "  \u2502"+strings.Repeat(" ", frameWidth-6)+"\u2502\n"))
	b.WriteString(t.color(6, 0, "  \u2514"+strings.Repeat("\u2500", frameWidth-6)+"\u2518\n"))
	return b.String()
}

const bubbleWidth = 62

func (t *TUI) renderBubble(m Message) string {
	var b strings.Builder
	width := bubbleWidth - 4
	switch m.Author {
	case AuthorUser:
		b.WriteString(t.color(6, 0, "  ┌"+strings.Repeat("─", 3)+" you "+strings.Repeat("─", width-8)+"┐\n"))
		for _, line := range wordWrap(m.Text, width-2) {
			spaces := width - 2 - visibleLen(line)
			if spaces < 0 {
				spaces = 0
			}
			b.WriteString(fmt.Sprintf("  │ %s%s │\n", line, strings.Repeat(" ", spaces)))
		}
		b.WriteString(t.color(6, 0, "  └"+strings.Repeat("─", width+2)+"┘\n"))
	case AuthorAgent:
		text := m.Text
		if m.Pending {
			text = t.color(3, 0, text)
		}
		b.WriteString(t.color(2, 0, "  ┌"+strings.Repeat("─", 3)+" starlight "+strings.Repeat("─", width-12)+"┐\n"))
		for _, line := range wordWrap(text, width-2) {
			spaces := width - 2 - visibleLen(line)
			if spaces < 0 {
				spaces = 0
			}
			b.WriteString(fmt.Sprintf("  │ %s%s │\n", line, strings.Repeat(" ", spaces)))
		}
		b.WriteString(t.color(2, 0, "  └"+strings.Repeat("─", width+2)+"┘\n"))
	case AuthorSystem:
		text := center(m.Text, width-2)
		spaces := width - 2 - visibleLen(text)
		if spaces < 0 {
			spaces = 0
		}
		b.WriteString(fmt.Sprintf("  │ %s%s │\n", t.color(8, 0, text), strings.Repeat(" ", spaces)))
	}
	return b.String()
}

func center(s string, w int) string {
	l := visibleLen(s)
	if l >= w {
		return s
	}
	left := (w - l) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-l-left)
}

func (t *TUI) visibleMessages() []Message {
	if len(t.messages) <= maxScrollback {
		return t.messages
	}
	return t.messages[len(t.messages)-maxScrollback:]
}

// footer: mode tabs + key hints + input prompt inside a frame.
func (t *TUI) footer() string {
	var b strings.Builder
	b.WriteString(t.color(6, 0, "  \u250C"+strings.Repeat("\u2500", frameWidth-6)+"\u2510\n"))
	b.WriteString(t.tabsLine())
	b.WriteString(t.keyHints())
	b.WriteString(t.inputPrompt())
	b.WriteString(t.color(6, 0, "  \u2514"+strings.Repeat("\u2500", frameWidth-6)+"\u2518\n"))
	return b.String()
}

func (t *TUI) tabsLine() string {
	var parts []string
	for _, s := range screenOrder {
		label := s.String()
		if t.screen == s {
			parts = append(parts, t.color(0, 6, "["+label+"]"))
		} else {
			parts = append(parts, t.color(8, 0, " "+label+" "))
		}
	}
	text := "\u2502 " + strings.Join(parts, " ") + " "
	pad := frameWidth - 6 - visibleLen(text) + 8 // adjust for ANSI
	if pad < 0 {
		pad = 0
	}
	return "  " + text + strings.Repeat(" ", pad) + "\u2502\n"
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
	text := "\u2502 " + strings.Join(colored, "  ") + " "
	pad := frameWidth - 6 - visibleLen(text) + len(colored)*10 // rough ANSI adjustment
	if pad < 0 {
		pad = 0
	}
	return "  " + text + strings.Repeat(" ", pad) + "\u2502\n"
}

func (t *TUI) inputPrompt() string {
	text := fmt.Sprintf("\u2502 %s > ", t.screen.String())
	pad := frameWidth - 6 - visibleLen(text)
	if pad < 0 {
		pad = 0
	}
	return "  " + t.color(7, 0, text) + strings.Repeat(" ", pad) + t.color(6, 0, "\u2502\n")
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
			if cur.Len()+visibleLen(word)+1 > width {
				if cur.Len() == 0 {
					lines = append(lines, string([]rune(word)[:width]))
					word = string([]rune(word)[width:])
					for visibleLen(word) > width {
						lines = append(lines, string([]rune(word)[:width]))
						word = string([]rune(word)[width:])
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
