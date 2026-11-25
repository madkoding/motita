package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStuckSeesALoopAndLeavesRealTextAlone(t *testing.T) {
	loops := map[string]string{
		"a phrase":      strings.Repeat("Let me think about this again. ", 20),
		"one character": strings.Repeat("a", 500),
		"a long unit":   strings.Repeat(strings.Repeat("x", 99)+"y", 9),
	}
	for name, text := range loops {
		if !stuck([]byte(text)) {
			t.Errorf("%s is a loop", name)
		}
	}
	fine := map[string]string{
		"short":       "ok",
		"prose":       strings.Repeat("a different sentence number ", 3) + "and then an end",
		"few copies":  strings.Repeat("Let me think about this again. ", 5),
		"table rule":  strings.Repeat("-", 80),
		"not at tail": strings.Repeat("loop ", 200) + "and then it said something else entirely, at length",
	}
	for name, text := range fine {
		if stuck([]byte(text)) {
			t.Errorf("%s is not a loop", name)
		}
	}
}

func TestRepeatWatchKeepsABoundedWindowAndLooksEverySoOften(t *testing.T) {
	var w repeatWatch
	if w.feed("abc") {
		t.Error("too little to look at")
	}
	loop := strings.Repeat("Let me think about this again. ", 30)
	hit := false
	for i := 0; i < len(loop); i += 40 {
		hit = hit || w.feed(loop[i:min(i+40, len(loop))])
	}
	if !hit {
		t.Error("the loop was never seen")
	}
	var big repeatWatch
	for range 200 {
		big.feed(strings.Repeat("0123456789", 100) + "!")
	}
	if len(big.tail) > repeatWindow {
		t.Errorf("window = %d", len(big.tail))
	}
}

func TestAStreamStuckInALoopIsCutAndNotRetried(t *testing.T) {
	c := clientFor(t, "http://127.0.0.1:1", nil)
	opened := 0
	cancelled := make(chan struct{})
	c.openStream = func(ctx context.Context, _ []Message, _ []Tool) (<-chan StreamChunk, error) {
		opened++
		ch := make(chan StreamChunk)
		go func() {
			defer close(ch)
			for {
				select {
				case ch <- StreamChunk{Event: StreamText, Text: "Let me think about this again. "}:
				case <-ctx.Done():
					close(cancelled)
					return
				}
			}
		}()
		return ch, nil
	}
	var last StreamChunk
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		last = chunk
	}
	if last.Event != StreamError || !errors.Is(last.Error, ErrRepeating) {
		t.Fatalf("last = %+v", last)
	}
	if opened != 1 {
		t.Errorf("a loop is not retried: opened %d times", opened)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Error("the provider's stream was not stopped")
	}
}
