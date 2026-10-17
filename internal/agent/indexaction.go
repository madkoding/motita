package agent

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/redact"
)

// indexOutputChars caps what one search_index answer puts in the model's context.
const indexOutputChars = 8000

// indexAction answers search_index: the built-in code index over the working directory, the same
// one plan mode offers. It reads and writes nothing in the workspace, so it is not a write.
func (rr *roundRun) indexAction(kind string, action Command) bool {
	if kind != "search_index" {
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, rr.a.searchIndex(rr, action.Command))
	rr.a.log.Info(rr.prefix+"index action", "query", truncate(firstLineOf(action.Command), 120))
	return true
}

// searchIndex runs one query and returns text for the model, an error included: a failed search
// is information for the model, not a failure of the run.
func (a *Agent) searchIndex(rr *roundRun, query string) string {
	query = strings.TrimSpace(query)
	if !a.cfg.CodeIndex.Enabled {
		return "Error: the code index is switched off (code_index.enabled); use grep through a command."
	}
	if query == "" {
		return "Error: 'command' must hold the question or symbol to search for."
	}
	root, err := a.ReadPath(".")
	if err != nil {
		return "Error: " + err.Error()
	}
	s := codeindex.Shared(a.cfg.CodeIndex.Options())
	hits, err := s.Search(rr.ctx, root, query, 0)
	if err != nil {
		return "Error searching the index: " + err.Error()
	}
	out := codeindex.Format(query, hits)
	if w := s.Warning(); w != "" {
		out += "\n(" + w + ")"
	}
	return truncateMiddle(redact.String(out), indexOutputChars)
}
