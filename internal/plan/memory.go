package plan

import (
	"strconv"

	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/redact"
)

// WithMemory gives the planner the person's memory, reached through recall and remember. Nil, the
// default, leaves it out.
func (p *Planner) WithMemory(m *memory.Store) *Planner {
	p.memory = m
	return p
}

// memoryArgs are the arguments of the memory tools.
type memoryArgs struct {
	Query     string `json:"query"`
	Namespace string `json:"namespace"`
	Entry     string `json:"entry"`
	Content   string `json:"content"`
	Graph     bool   `json:"graph"`
}

// searchQuery is the query with the collection and the graph option written as filters.
func (a memoryArgs) searchQuery() string {
	q := a.Query
	if a.Namespace != "" {
		q += " ns:" + strconv.Quote(a.Namespace)
	}
	if a.Graph {
		q += " graph:1"
	}
	return q
}

// toolMemory runs one of the memory tools.
func (p *Planner) toolMemory(name string, raw []byte) string {
	if p.memory == nil {
		return "Error: there is no memory in this session."
	}
	var args memoryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	switch name {
	case "search_memory":
		return limitString(p.memory.SearchText(args.searchQuery()), maxToolOutputBytes)
	case "read_memory":
		return limitString(p.memory.ReadText(args.Entry), maxToolOutputBytes)
	case "write_memory":
		return p.saved(p.memory.WriteText(args.Entry, redact.String(args.Content)))
	default: // append_memory
		return p.saved(p.memory.AppendText(args.Entry, redact.String(args.Content)))
	}
}

// saved tells the interface what a write or an append put in the memory, and hands the reply on.
func (p *Planner) saved(reply string) string {
	if line, ok := memory.SavedLine(reply); ok {
		p.tracef("%s", line)
	}
	return reply
}
