package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestAnUnattendedRunDoesNotStopToAsk: a scheduled run has nobody to answer, so an analysis that
// would ask proceeds on its assumption even on an agent built as interactive. The same analysis
// asks when the context is not marked (TestAListOnlyAskIsNotFailed).
func TestAnUnattendedRunDoesNotStopToAsk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestText(r, "## ANALYSIS OF THE TASK") {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"ask\",\"understandable\":false,\"questions\":[`+
				`{\"text\":\"where to?\",\"assumption\":\"keep it local\"},`+
				`{\"text\":\"which order?\"}]}"}}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true
	var got TaskResult
	e.agent.SetObserver(func(tr TaskResult) { got = tr })
	_ = e.agent.Run(WithUnattended(context.Background()))
	if got.NeedsInput || got.Question != "" {
		t.Fatalf("an unattended run must not end asking: %+v", got)
	}
	if e.agent.Interactive {
		t.Error("the agent of an unattended run must not be interactive")
	}
}

// TestUnattendedIsOnlyWhatWasMarked: a plain context is attended.
func TestUnattendedIsOnlyWhatWasMarked(t *testing.T) {
	if Unattended(context.Background()) {
		t.Error("a context nobody marked is not unattended")
	}
	if !Unattended(WithUnattended(context.Background())) {
		t.Error("a marked context is unattended")
	}
}

// requestText reports whether the body of a chat request carries marker. The body is put back, so
// a handler can ask about several markers.
func requestText(r *http.Request, marker string) bool {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	return strings.Contains(string(body), marker)
}
