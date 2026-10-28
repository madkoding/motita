package plan

import (
	"strconv"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/redact"
)

// WithMemory gives the planner the person's memory, which offers the memory tools. Nil, the
// default, leaves them out.
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

// memoryTools describes the tools over the person's memory.
func memoryTools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("search_memory", "Searches the person's memory (their second brain: plain Markdown organised in "+
			"collections) by the words they use and by meaning, and returns the matching sections as [[Title]] with "+
			"their path, author and date. Use it before answering anything about the person's own facts, decisions, "+
			"plans or projects. Narrow it with namespace (a collection) or with author:, since: and until: in the "+
			"query; set graph for a question about how things relate, to also get the entries linked to the best answers.",
			llm.ObjectSchema(map[string]any{
				"query": llm.StringProperty(
					"The words to look for, with optional author:Name since:2026-01-31 until:2026-12-31."),
				"namespace": llm.StringProperty(
					"Only this collection (the first folder of an entry), for example \"Engineering\"."),
				"graph": map[string]any{
					"type": "boolean", "description": "Also return the entries linked to the best answers.",
				},
			}, "query")),
		llm.NewTool("read_memory", "Reads one entry in full, by title or path, with the entries it links to and the "+
			"entries that link to it. Cite what you take from it as [[Title]].",
			llm.ObjectSchema(map[string]any{
				"entry": llm.StringProperty("The entry's title or path, as search_memory returned it."),
			}, "entry")),
		llm.NewTool("write_memory", "Creates a new entry (never replaces one) in a collection. Use headings, one "+
			"subject per entry, and link related entries with [[Title]].",
			llm.ObjectSchema(map[string]any{
				"entry":   llm.StringProperty("Title or path of the new entry, for example \"Engineering/Rollback policy\"."),
				"content": llm.StringProperty("The entry's Markdown."),
			}, "entry", "content")),
		llm.NewTool("append_memory", "Adds text to the end of an entry (creating it when it does not exist). "+
			"This is how to record a decision or a fact without losing what the person wrote.",
			llm.ObjectSchema(map[string]any{
				"entry":   llm.StringProperty("The entry's title or path."),
				"content": llm.StringProperty("The Markdown to add."),
			}, "entry", "content")),
	}
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
		return p.memory.WriteText(args.Entry, redact.String(args.Content))
	default: // append_memory
		return p.memory.AppendText(args.Entry, redact.String(args.Content))
	}
}
