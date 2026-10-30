package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/knowledge"
)

// TestATaskLearnsAndTheNextOneIsTold: the report's lessons are written to the project and the
// next task's prompts carry them.
func TestATaskLearnsAndTheNextOneIsTold(t *testing.T) {
	var analyses []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		switch {
		case strings.Contains(text, "## ANALYSIS OF THE TASK"):
			analyses = append(analyses, text)
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"task\",\"understandable\":true,`+
				`\"summary\":\"s\",\"plan\":[\"do\"]}"}}]}`)
		case strings.Contains(text, "## PREVIOUS ROUNDS"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"reasoning\":\"r\",\"actions\":[],\"done\":true,`+
				`\"final_action\":{\"command\":\"true\"}}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"version\":1,\"status\":\"done\",\"summary\":\"ok\",`+
				`\"lessons\":[\"tests run with make test\"]}"}}]}`)
		}
	}))
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.cfg.Agent.CriteriaAudit = false
	_ = e.agent.Run(context.Background())
	if got := knowledge.Lessons(e.dir); len(got) != 1 || got[0] != "tests run with make test" {
		t.Fatalf("the lesson must be in the project: %q", got)
	}
	// A second agent over the same workspace reads it in its first prompt.
	second := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second},
		func(c *config.Config) { c.Agent.WorkspaceDir = e.dir })
	second.agent.cfg.Agent.CriteriaAudit = false
	analyses = nil
	_ = second.agent.Run(context.Background())
	if len(analyses) == 0 || !strings.Contains(analyses[0], "tests run with make test") {
		t.Fatalf("the next task must be told what was learned: %d analyses", len(analyses))
	}
	// A report without lessons, or no report, writes nothing and fails nothing; a workspace that
	// cannot be written is only warned about.
	e.agent.saveLessons(nil)
	e.agent.saveLessons(&Report{})
	blocked := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocked, []byte("x"), 0o600)
	e.agent.cfg.Agent.WorkspaceDir = blocked
	e.agent.saveLessons(&Report{Lessons: []string{"x"}})
}
