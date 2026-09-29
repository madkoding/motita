package agent

// The model's reasoning is visible WHILE it is written.
//
// Reported from real use: "I still cannot see what the model decides, reasons or thinks while
// I wait - only states". A phase takes tens of seconds and all the interface could show was
// "deciding action...". These tests pin the live view: snapshots of what the model is writing
// (LivePrefix), and each phase's finished reasoning, whole (ThinkingPrefix).

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

// TestTheReasoningIsShownWhileItIsWritten: a real streamed run. The user sees the model's
// thinking tokens, its reasoning being written, the plan, and what the commands printed.
func TestTheReasoningIsShownWhileItIsWritten(t *testing.T) {
	old := liveInterval
	liveInterval = 0
	defer func() { liveInterval = old }()

	s := &scriptServer{sse: true, execute: func(int, string) string {
		return mustJSON(map[string]any{
			"reasoning": "the file has to exist, so I create it and check it",
			"actions":   []map[string]string{{"kind": "command", "command": "echo created-it"}},
			"done":      true,
		})
	}}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	var mu sync.Mutex
	var lines []string
	e.agent.SetProgress(func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	e.agent.SetLiveThinking(true)
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		LivePrefix + "weighing the options",                      // thinking tokens, before any answer
		LivePrefix + "the file has to",                           // the reasoning, half written
		ThinkingPrefix + "the file has to exist, so I create it", // the reasoning, whole
		ThinkingPrefix + "plan:\n1. do it",                       // the plan
		ThinkingPrefix + "done means:\n- it is done",             // the success criteria
		"output (exit 0):\ncreated-it",                           // what the command printed
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the progress never showed %q:\n%s", want, all)
		}
	}
}

// TestLiveSnapshotsAreThrottledAndNeverRepeated: a snapshot per fragment would flood the
// interface; the same snapshot twice says nothing new.
func TestLiveSnapshotsAreThrottledAndNeverRepeated(t *testing.T) {
	var got []string
	a := &Agent{Progress: func(f string, args ...any) { got = append(got, fmt.Sprintf(f, args...)) }}

	old := liveInterval
	liveInterval = 0
	l := &liveView{a: a, phase: "execute"}
	l.add("thinking hard", true)
	l.add("", true) // nothing new
	liveInterval = time.Hour
	l.add(" still", true) // too soon
	liveInterval = old
	if len(got) != 1 || got[0] != LivePrefix+"thinking hard" {
		t.Errorf("snapshots = %q", got)
	}
	// A phase with nothing readable yet reports nothing.
	liveInterval = 0
	defer func() { liveInterval = old }()
	empty := &liveView{a: a, phase: "execute"}
	empty.add(`{"actions":[`, false)
	if len(got) != 1 {
		t.Errorf("an unreadable fragment must not be reported: %q", got)
	}
}

// TestLiveTextReadsHalfWrittenJSON: the readable fields are read out of a reply cut anywhere.
func TestLiveTextReadsHalfWrittenJSON(t *testing.T) {
	cases := []struct{ phase, thought, answer, want string }{
		{"execute", "", `{"reasoning": "I will lis`, "I will lis"},
		{"execute", "", `{"reasoning":"a","actions":[{"command":"ls -la"},{"command":"pw`, "a\n$ ls -la\n$ pw"},
		{"plan", "", `{"plan":[{"step":1,"action":"read it"},{"step":2,"action":"fix`, "- read it\n- fix"},
		{"analyze", "", `{"kind":"chat","reply":"hola, ¿qué tal?"}`, "hola, ¿qué tal?"}, // spanish-fixture: a reply
		{"execute", "", "Let me think about this first.", "Let me think about this first."},
		{"execute", "deep thought", `{"actions":`, "deep thought"},
		{"execute", "", "```json\n{", ""},
		{"unknown", "", `{"x":"y"}`, ""},
	}
	for _, c := range cases {
		if got := liveText(c.phase, c.thought, c.answer); got != c.want {
			t.Errorf("liveText(%s, %q) = %q, want %q", c.phase, c.answer, got, c.want)
		}
	}
	long := liveText("execute", strings.Repeat("x", liveMaxRunes+10)+"END", "")
	if !strings.HasPrefix(long, "…") || !strings.HasSuffix(long, "END") || len([]rune(long)) != liveMaxRunes+1 {
		t.Errorf("a long snapshot keeps its tail: %d runes", len([]rune(long)))
	}
}

// TestPartialStringsDecodesEscapesAndCutOffs: every escape, and every place a reply can end.
func TestPartialStringsDecodesEscapesAndCutOffs(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{`{"f": "a\nb\tc\rd\"e\\f\/g\u00e9h"}`, []string{"a\nb\tcd\"e\\f/géh"}},
		{`{"f":"cut \`, []string{"cut "}},
		{`{"f":"cut \u00`, []string{"cut "}},
		{`{"f":"bad \uzzzzok"}`, []string{"bad ok"}},
		{`{"f" 1, "f": 2, "f":`, nil},
		{`{"f"`, nil},
		{`{"f": "one"} {"f" :` + "\n" + ` "two`, []string{"one", "two"}},
	}
	for _, c := range cases {
		got := partialStrings(c.text, "f")
		if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("partialStrings(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
