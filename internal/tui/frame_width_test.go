package tui

import (
	"strings"
	"testing"
)

// The input box must stay on the same rows however the requests look. Text that the width
// count measured narrower than the terminal drew it (CJK, emoji, tabs) was wrapped by the
// terminal onto an extra row, the frame outgrew the window and the box moved after each request.

var awkwardTexts = []string{
	"plain request",
	"columnas:\tuno\tdos\ttres",
	"日本語のテキストがここにあります、長い行を折り返します。日本語のテキスト日本語のテキスト",
	"hecho ✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅✅ 🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀",
	"ééé combinados ééééééééééé",
}

func TestFrameKeepsItsShapeWhateverTheRequestsLookLike(t *testing.T) {
	for _, w := range []int{minWidth, 60, 80, 120} {
		for _, h := range []int{16, 24, 40} {
			checkFrameShape(t, w, h)
		}
	}
}

// checkFrameShape feeds every awkward text through a TUI of the given size and checks each frame.
func checkFrameShape(t *testing.T, w, h int) {
	t.Helper()
	tu := newFakeTUI("", &fakeRunner{})
	tu.Width, tu.Height = w, h
	boxTop := -1
	for n, s := range awkwardTexts {
		tu.addMessage(AuthorUser, s)
		tu.addMessage(AuthorAgent, s)
		tu.draft = s
		tu.busy = n%2 == 0
		lines, _ := tu.layout(w, h)
		if len(lines) != h {
			t.Fatalf("%dx%d request %d: frame has %d rows, want %d", w, h, n, len(lines), h)
		}
		checkRows(t, lines, w, n)
		// The box's top border is the row that carries the mode: it must not move.
		top := boxTopRow(lines)
		if boxTop >= 0 && top != boxTop {
			t.Fatalf("%dx%d request %d: input box moved from row %d to %d", w, h, n, boxTop, top)
		}
		boxTop = top
	}
}

// checkRows fails on a row that is wider than the terminal or holds a tab or newline.
func checkRows(t *testing.T, lines []string, w, n int) {
	t.Helper()
	for i, l := range lines {
		if got := visibleLen(l); got > w-1 {
			t.Fatalf("width %d request %d row %d is %d columns wide, limit %d: %q", w, n, i, got, w-1, stripANSI(l))
		}
		if strings.ContainsAny(l, "\t\n") {
			t.Fatalf("width %d request %d row %d holds a tab or newline: %q", w, n, i, l)
		}
	}
}

// boxTopRow is the row of the input box's top border.
func boxTopRow(lines []string) int {
	top := -1
	for i, l := range lines {
		if strings.Contains(stripANSI(l), glyphTopLeft) {
			top = i
		}
	}
	return top
}

func TestRuneWidth(t *testing.T) {
	for r, want := range map[rune]int{'a': 1, 'é': 1, '日': 2, '한': 2, '🚀': 2, '✅': 2, '́': 0, '‍': 0, '️': 0, '…': 1} {
		if got := runeWidth(r); got != want {
			t.Errorf("runeWidth(%q) = %d, want %d", r, got, want)
		}
	}
}

func TestSplitAtWidthDoesNotStraddleAWideRune(t *testing.T) {
	cut, piece := splitAtWidth("a日本", 2)
	if piece != "a" || cut != 1 {
		t.Errorf("splitAtWidth = %d %q, want 1 \"a\"", cut, piece)
	}
	// A rune wider than the whole line is still taken, or the caller's loop would not end.
	if cut, piece = splitAtWidth("日本", 1); piece != "日" || cut != len("日") {
		t.Errorf("splitAtWidth = %d %q, want the first rune", cut, piece)
	}
	// A combining mark stays with its base at the cut.
	if _, piece = splitAtWidth("éx", 1); piece != "é" {
		t.Errorf("splitAtWidth = %q, want the base with its mark", piece)
	}
}

func TestWrapVisibleMeasuresColumnsAndSkipsWholeEscapes(t *testing.T) {
	for _, row := range wrapVisible("日本語日本語日本語", 6) {
		if visibleLen(row) > 6 {
			t.Errorf("row %q is %d columns, limit 6", row, visibleLen(row))
		}
	}
	coloured := "\x1b[38;5;75m" + strings.Repeat("a", 10) + "\x1b[0m"
	rows := wrapVisible(coloured, 10)
	if len(rows) != 1 {
		t.Errorf("a coloured line that fits was wrapped into %d rows", len(rows))
	}
}

func TestClipLineCutsOnColumns(t *testing.T) {
	if got := clipLine("日本語日本語", 5); visibleLen(got) > 5 {
		t.Errorf("clipLine = %q (%d columns), limit 5", got, visibleLen(got))
	}
	if got := clipLine("日本語日本語", 1); got != "…" {
		t.Errorf("clipLine to one column = %q", got)
	}
}

func TestGuardRowsExpandsTabsAndClipsColouredRows(t *testing.T) {
	rows := guardRows([]string{"a\tb", "\x1b[31m" + strings.Repeat("x", 30) + "\x1b[0m", "ok"}, 10)
	if rows[0] != "a    b" || rows[2] != "ok" {
		t.Errorf("rows = %q", rows)
	}
	if visibleLen(rows[1]) != 10 || !strings.HasSuffix(rows[1], "\x1b[0m") {
		t.Errorf("coloured row = %q", rows[1])
	}
}
