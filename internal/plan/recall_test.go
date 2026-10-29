package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/session"
)

// recordedMessage is one message of a request as the mock provider saw it.
type recordedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// recordingServer answers every request with an answer and keeps the messages it was sent.
func recordingServer(t *testing.T, answers ...string) (*httptest.Server, *[][]recordedMessage) {
	t.Helper()
	var requests [][]recordedMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []recordedMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding the request: %v", err)
		}
		requests = append(requests, req.Messages)
		answer := "(no answer)"
		if len(answers) >= len(requests) {
			answer = answers[len(requests)-1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": answer}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// TestRecallIsSentButNotKeptInTheSession: the block belongs to the turn that asked for it. It
// goes among the leading system messages, before the conversation, and it is never written to
// the session: the next turn recalls its own, and a stored copy would make the archive recall
// itself.
func TestRecallIsSentButNotKeptInTheSession(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv, requests := recordingServer(t, "the first answer", "the second answer")
	p := New(newClient(t, srv), a).WithSessionPolicy("gpt-4o", 128000, 1000, 0.5, 2)

	var live []int
	var trace []string
	p.WithTrace(func(format string, args ...any) { trace = append(trace, fmt.Sprintf(format, args...)) }).
		WithRecall(func(_ context.Context, query string, shown int) (string, int) {
			live = append(live, shown)
			return "ARCHIVED-BLOCK for " + query, 2
		})

	if _, err := p.Run(context.Background(), "the first question"); err != nil {
		t.Fatalf("the first turn: %v", err)
	}
	if _, err := p.Run(context.Background(), "the second question"); err != nil {
		t.Fatalf("the second turn: %v", err)
	}

	if len(*requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(*requests))
	}
	first := (*requests)[0]
	// The system prompt opens the conversation; the recall follows it, before the question.
	if len(first) < 3 || first[0].Role != "system" || first[1].Role != "system" {
		t.Fatalf("the recall was not placed among the system messages: %+v", first)
	}
	if !strings.Contains(first[1].Content, "ARCHIVED-BLOCK for the first question") {
		t.Errorf("the recalled block is not in the request: %+v", first)
	}
	if last := first[len(first)-1]; last.Role != "user" || !strings.Contains(last.Content, "the first question") {
		t.Errorf("the question does not close the request: %+v", first)
	}

	// The second request carries the SECOND block, once: the first turn's is gone.
	second := (*requests)[1]
	var blocks int
	for _, m := range second {
		if strings.Contains(m.Content, "ARCHIVED-BLOCK") {
			blocks++
			if !strings.Contains(m.Content, "the second question") {
				t.Errorf("an old recall survived into the next turn: %q", m.Content)
			}
		}
	}
	if blocks != 1 {
		t.Errorf("the second request carries %d recalled blocks, want 1: %+v", blocks, second)
	}

	// And none of it is part of what the session keeps.
	for _, m := range p.Session().Messages() {
		if strings.Contains(m.Content, "ARCHIVED-BLOCK") {
			t.Errorf("the recall was written to the session: %q", m.Content)
		}
	}

	// live is the number of earlier turns still shown verbatim: none on the first turn, one
	// on the second.
	if len(live) != 2 || live[0] != 0 || live[1] != 1 {
		t.Errorf("live = %v, want [0 1]", live)
	}
	if !strings.Contains(strings.Join(trace, "\n"), "[recalled 2 passages from earlier]") {
		t.Errorf("the recall was not traced: %v", trace)
	}
}

// TestRecallWithNothingFoundLeavesTheRequestAlone: a hook that finds nothing must not add an
// empty system message or a trace line.
func TestRecallWithNothingFoundLeavesTheRequestAlone(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv, requests := recordingServer(t, "an answer")
	p := New(newClient(t, srv), a).WithSessionPolicy("gpt-4o", 128000, 1000, 0.5, 2)

	var trace []string
	p.WithTrace(func(format string, args ...any) { trace = append(trace, fmt.Sprintf(format, args...)) }).
		WithRecall(func(context.Context, string, int) (string, int) { return "", 0 })

	if _, err := p.Run(context.Background(), "the question"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*requests) != 1 || len((*requests)[0]) != 2 {
		t.Fatalf("the empty recall added a message: %+v", *requests)
	}
	if strings.Contains(strings.Join(trace, "\n"), "recalled") {
		t.Errorf("an empty recall was traced: %v", trace)
	}
}

func TestRecallForCountsTheTurnsStillShown(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv, _ := recordingServer(t, "an answer")
	p := New(newClient(t, srv), a)

	// No session yet and no user message: the count cannot go below zero.
	empty := session.New("mock", "system", 128000)
	p.turnRecall = "stale"
	p.recallFor(context.Background(), empty, "the question")
	if p.turnRecall != "" {
		t.Errorf("with no hook the block survived: %q", p.turnRecall)
	}

	var got int
	p.WithRecall(func(_ context.Context, _ string, live int) (string, int) {
		got = live
		return "", 0
	})
	p.recallFor(context.Background(), empty, "the question")
	if got != 0 {
		t.Errorf("live with no user message = %d, want 0", got)
	}

	empty.Append(llm.Message{Role: "user", Content: "one"})
	empty.Append(llm.Message{Role: "assistant", Content: "answer"})
	empty.Append(llm.Message{Role: "user", Content: "two"})
	p.recallFor(context.Background(), empty, "three")
	if got != 1 {
		t.Errorf("live with two user messages = %d, want 1", got)
	}
}

func TestWithRecallPlacesTheBlockAfterTheLeadingSystemMessages(t *testing.T) {
	p := &Planner{}
	msgs := []llm.Message{
		{Role: "system", Content: "one"},
		{Role: "system", Content: "two"},
		{Role: "user", Content: "question"},
	}

	if got := p.withRecall(msgs); &got[0] != &msgs[0] {
		t.Error("with nothing recalled it must return the slice it was given")
	}

	p.turnRecall = "ARCHIVED"
	out := p.withRecall(msgs)
	if len(out) != len(msgs)+1 {
		t.Fatalf("messages = %d, want %d", len(out), len(msgs)+1)
	}
	if out[2].Role != "system" || out[2].Content != "ARCHIVED" {
		t.Errorf("the block is not after the leading system messages: %+v", out)
	}
	if out[3].Content != "question" {
		t.Errorf("the conversation was displaced: %+v", out)
	}

	// A request that is ALL system messages gets the block at the end, after all of them.
	allSystem := []llm.Message{{Role: "system", Content: "only"}}
	out = p.withRecall(allSystem)
	if len(out) != 2 || out[1].Content != "ARCHIVED" {
		t.Errorf("the block of an all-system request = %+v", out)
	}
}
