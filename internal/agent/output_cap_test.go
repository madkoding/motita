package agent

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// The report of a command is what the interface shows for it AND what the terminal drawer replays
// as the record of the run. It used to be cut to 800 bytes by dropping the middle, so a `grep`
// over a tree arrived in the chat and in the drawer as a few lines and a bare "[... middle
// omitted ...]" - which reads as the command's whole output. Measured on the reader's own
// sessions: 49 kept steps carried that mark at exactly 829 bytes each.
func TestACommandsOutputIsReportedWhole(t *testing.T) {
	// A real grep over a repo: far bigger than the 800 bytes this report used to be cut to, and
	// comfortably inside the cap (and inside the sandbox's own 64 KB limit in this fixture), so
	// nothing but the report's own cap could cut it.
	const lines = 700
	want := "first-line-marker\n" + strings.Repeat("a line of grepped output with some length\n", lines) + "last-line-marker\n"
	if len(want)*1 >= terminalOutputChars {
		t.Fatalf("the fixture must fit under the cap: %d >= %d", len(want), terminalOutputChars)
	}
	if len(want) < 800*8 {
		t.Fatalf("the fixture must be far past the 800 bytes the report used to keep: %d", len(want))
	}

	s := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo " + shellQuoteText(want)}},
		donePerAttempt:    []bool{true},
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var mu sync.Mutex
	var got []string
	e.agent.SetProgress(func(format string, args ...any) {
		mu.Lock()
		got = append(got, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	reported := ""
	for _, line := range got {
		if strings.HasPrefix(line, "output (exit ") {
			reported += line
		}
	}
	if reported == "" {
		t.Fatal("the command's output was never reported")
	}
	if !strings.Contains(reported, "first-line-marker") || !strings.Contains(reported, "last-line-marker") {
		t.Errorf("the report is missing the ends of the output:\n%s", reported[:min(400, len(reported))])
	}
	if strings.Contains(reported, "middle omitted") {
		t.Errorf("the report was cut although it fits in the cap (%d bytes)", len(reported))
	}
	if n := strings.Count(reported, "a line of grepped output"); n != lines {
		t.Errorf("the report kept %d of %d lines", n, lines)
	}
}

// A cut has to SAY that it is one, and how much is missing: the marker used to name neither, so a
// reader could not tell a short output from a truncated one.
func TestACutOutputSaysHowMuchIsMissing(t *testing.T) {
	long := "HEAD\n" + strings.Repeat("x", 200000) + "\nTAIL"
	got := truncateMiddle(long, terminalOutputChars)
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") {
		t.Error("both ends must survive the cut")
	}
	if !strings.Contains(got, "bytes omitted") || !strings.Contains(got, fmt.Sprintf("of %d bytes", len(long))) {
		t.Errorf("the marker must say how much of how many was dropped: %q", got[len(got)-60:])
	}
	// The marker itself is extra: the cap bounds what is KEPT, not the note that says what was cut.
	if len(got) > terminalOutputChars+80 {
		t.Errorf("the cut kept %d bytes, more than the %d it was given", len(got), terminalOutputChars)
	}
}

// shellQuoteText wraps text so `echo <text>` prints it byte for byte, for building a fixture.
func shellQuoteText(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
