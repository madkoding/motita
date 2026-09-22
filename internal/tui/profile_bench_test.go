package tui

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// benchTUI builds a TUI with a realistic frame: a conversation, a draft in the box and the body
// padded to the bottom of the window (which is what drawFrame's cursor arithmetic has to handle).
func benchTUI(body int) (*TUI, *bytes.Buffer) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	tu.draft = "type something here"
	for i := 0; i < body; i++ {
		tu.messages = append(tu.messages, Message{
			Author: AuthorUser,
			Text: "a line of conversation with enough text to wrap over several rows " +
				"and force the whole formatting path of the renderer",
		})
		tu.messages = append(tu.messages, Message{
			Author: AuthorAgent,
			Text: "assistant reply with long text, accents and emoji \U0001F31F to cover " +
				"the width path in runes and not in bytes, which is where these things break",
		})
	}
	tu.drawFrame() // settle: the first frame is always full
	out.Reset()
	return tu, &out
}

// BenchmarkLayout measures building the frame: layout, wrapping and colouring.
// It runs on every keystroke, so its cost is what the user feels while typing.
func BenchmarkLayout(b *testing.B) {
	for _, body := range []int{0, 20, 100} {
		b.Run("body="+strconv.Itoa(body), func(b *testing.B) {
			tu, _ := benchTUI(body)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tu.layout(tu.Width, tu.Height)
			}
		})
	}
}

// BenchmarkDrawFrameTyping measures the whole per-keystroke cost: build, diff and write.
//
// The draft is reset every run: letting it grow without bound made the later iterations lay out
// a much longer line, so the numbers drifted with the iteration count instead of measuring one
// keystroke at a fixed size.
func BenchmarkDrawFrameTyping(b *testing.B) {
	tu, out := benchTUI(20)
	base := tu.draft
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%64 == 0 {
			tu.draft = base // same line length for every sample
		}
		tu.draft += "x"
		tu.drawFrame()
		out.Reset()
	}
}

// BenchmarkMessageLines isolates the wrapping and colouring of one block.
func BenchmarkMessageLines(b *testing.B) {
	tu, _ := benchTUI(0)
	m := Message{
		Author: AuthorAgent,
		Text: strings.Repeat(
			"reply with accents \U0001F31F and enough text to wrap several times ", 20),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tu.messageLines(m, 100)
	}
}
