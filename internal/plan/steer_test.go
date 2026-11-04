package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/llm"
)

// steeredEngine writes part of an answer and waits, the way a slow model does; the test writes to
// the turn meanwhile. The second request is answered at once.
type steeredEngine struct {
	writing chan struct{}
	asked   [][]llm.Message
}

func (e *steeredEngine) CompleteToolsStream(
	ctx context.Context, msgs []llm.Message, _ []llm.Tool,
) <-chan llm.StreamChunk {
	e.asked = append(e.asked, msgs)
	ch := make(chan llm.StreamChunk, 3)
	if len(e.asked) == 1 {
		go func() {
			defer close(ch)
			ch <- llm.StreamChunk{Event: llm.StreamText, Text: "half an answer"}
			close(e.writing)
			<-ctx.Done()
			ch <- llm.StreamChunk{Event: llm.StreamError, Error: ctx.Err()}
		}()
		return ch
	}
	ch <- llm.StreamChunk{Event: llm.StreamText, Text: "the answer to both"}
	ch <- llm.StreamChunk{Event: llm.StreamDone}
	close(ch)
	return ch
}

func (e *steeredEngine) Complete(context.Context, []llm.Message) (string, error) { return "", nil }

// A message sent while the model writes cuts the reply: the round is asked again with the message
// in the conversation, and the unfinished text is dropped from what the page shows.
func TestAMessageInterruptsThePlanTurn(t *testing.T) {
	a, _ := makeAgent(t, true)
	engine := &steeredEngine{writing: make(chan struct{})}
	var streamed []string
	p := New(engine, a).WithLoops(1).WithStream(func(s string) { streamed = append(streamed, s) })
	if p.Steer("too early") {
		t.Fatal("no turn is running")
	}
	go func() {
		<-engine.writing
		p.Steer("use the short version")
	}()
	out, err := p.Run(context.Background(), "explain the build")
	if err != nil || out != "the answer to both" {
		t.Fatalf("answer %q, err %v", out, err)
	}
	if len(engine.asked) != 2 {
		t.Fatalf("the round must be asked again once, asked %d times", len(engine.asked))
	}
	last := engine.asked[1][len(engine.asked[1])-1]
	if last.Role != "user" || !strings.Contains(last.Content, "use the short version") {
		t.Fatalf("the second request must end with the message, got %+v", last)
	}
	for _, m := range p.Session().Messages() {
		if strings.Contains(m.Content, "half an answer") {
			t.Fatal("the unfinished reply must not stay in the conversation")
		}
	}
	if !strings.Contains(strings.Join(streamed, "|"), StreamReset) {
		t.Errorf("the page must be told to drop the unfinished text: %q", streamed)
	}
	if p.Steer("after") || p.UnreadSteering() != nil {
		t.Fatal("an ended turn takes nothing and left nothing unread")
	}
}
