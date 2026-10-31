package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestAMalformedAnalysisIsAskedForOnceMore: one reply that is not the JSON object no longer ends
// the turn with "I could not read the request"; the second reply, told what was wrong, is used.
func TestAMalformedAnalysisIsAskedForOnceMore(t *testing.T) {
	var analyses atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case requestText(r, "Your last reply could not be used"):
			analyses.Add(1)
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"chat\",\"understandable\":true,`+
				`\"reply\":\"hello there\"}"}}]}`)
		case requestText(r, "## ANALYSIS OF THE TASK"):
			analyses.Add(1)
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Sure! Here is my analysis of the task."}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
		}
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true
	var got TaskResult
	e.agent.SetObserver(func(tr TaskResult) { got = tr })
	_ = e.agent.Run(context.Background())
	if n := analyses.Load(); n != 2 {
		t.Fatalf("the analysis was asked %d times, want the first try and one more", n)
	}
	if got.NeedsInput || got.Reply != "hello there" {
		t.Errorf("the second analysis should be the one used: %+v", got)
	}
}
