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
	"github.com/madkoding/motita/internal/memory"
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

// recallRig is the planner after two turns that each recalled a block, with what the provider
// was sent and what the recall hook was told.
type recallRig struct {
	planner  *Planner
	requests [][]recordedMessage
	live     []int
	trace    []string
}

func recallAfterTwoTurns(t *testing.T) *recallRig {
	t.Helper()
	a, _ := makeAgent(t, true)
	srv, requests := recordingServer(t, "the first answer", "the second answer")
	p := New(newClient(t, srv), a).WithSessionPolicy("gpt-4o", 128000, 1000, 0.5, 2)

	rig := &recallRig{planner: p}
	p.WithTrace(func(format string, args ...any) {
		rig.trace = append(rig.trace, fmt.Sprintf(format, args...))
	}).WithRecall(func(_ context.Context, query string, shown int) (string, int) {
		rig.live = append(rig.live, shown)
		return "ARCHIVED-BLOCK for " + query, 2
	})

	for _, question := range []string{"the first question", "the second question"} {
		if _, err := p.Run(context.Background(), question); err != nil {
			t.Fatalf("%s: %v", question, err)
		}
	}
	rig.requests = *requests
	return rig
}

// TestRecallIsSentWithTheTurn: the block belongs to the turn that asked for it. It goes among
// the leading system messages, before the conversation, with the question closing the request.
func TestRecallIsSentWithTheTurn(t *testing.T) {
	rig := recallAfterTwoTurns(t)
	if len(rig.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(rig.requests))
	}
	first := rig.requests[0]
	if len(first) < 3 || first[0].Role != "system" || first[1].Role != "system" {
		t.Fatalf("the recall was not placed among the system messages: %+v", first)
	}
	if !strings.Contains(first[1].Content, "ARCHIVED-BLOCK for the first question") {
		t.Errorf("the recalled block is not in the request: %+v", first)
	}
	if last := first[len(first)-1]; last.Role != "user" || !strings.Contains(last.Content, "the first question") {
		t.Errorf("the question does not close the request: %+v", first)
	}
}

// TestRecallIsNotKeptInTheSession: the next turn recalls its own, a stored copy would make the
// archive recall itself, and none of it is part of what the session keeps. live is the number
// of earlier turns still shown verbatim: none on the first turn, one on the second.
func TestRecallIsNotKeptInTheSession(t *testing.T) {
	rig := recallAfterTwoTurns(t)
	if len(rig.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(rig.requests))
	}
	second := rig.requests[1]
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
	for _, m := range rig.planner.Session().Messages() {
		if strings.Contains(m.Content, "ARCHIVED-BLOCK") {
			t.Errorf("the recall was written to the session: %q", m.Content)
		}
	}
	if len(rig.live) != 2 || rig.live[0] != 0 || rig.live[1] != 1 {
		t.Errorf("live = %v, want [0 1]", rig.live)
	}
	if !strings.Contains(strings.Join(rig.trace, "\n"), "[recalled 2 passages from earlier]") {
		t.Errorf("the recall was not traced: %v", rig.trace)
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

// TestTheTurnStartsWithWhatTheMemoryKnows: the planner looks the memory up itself, before the
// model decides whether to, and the block is not kept in the session.
func TestTheTurnStartsWithWhatTheMemoryKnows(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv, requests := recordingServer(t, "an answer")
	mem := memory.Open(t.TempDir())
	if r := mem.WriteText("Deploys", "Deploys go out through the blue pipeline."); strings.HasPrefix(r, "Error") {
		t.Fatal(r)
	}
	var trace []string
	p := New(newClient(t, srv), a).WithMemory(mem).WithTrace(func(format string, args ...any) {
		trace = append(trace, fmt.Sprintf(format, args...))
	})
	if _, err := p.Run(context.Background(), "how do deploys go out?"); err != nil {
		t.Fatal(err)
	}
	first := (*requests)[0]
	if !strings.Contains(first[1].Content, "[[Deploys]]") || first[1].Role != "system" {
		t.Errorf("the memory was not brought to the turn: %+v", first)
	}
	if !strings.Contains(strings.Join(trace, "\n"), "[recalled 1 passages from memory]") {
		t.Errorf("trace = %v", trace)
	}
	for _, m := range p.Session().Messages() {
		if strings.Contains(m.Content, "[[Deploys]]") {
			t.Errorf("the memory block was written to the session: %q", m.Content)
		}
	}
}
