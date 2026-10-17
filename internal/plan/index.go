package plan

import (
	"context"
	"strings"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/xerj"
)

// IndexSearcher is what search_index asks: a local XERJ node that indexed a folder. The real
// *xerj.Client implements it.
type IndexSearcher interface {
	Search(ctx context.Context, query string, size int) ([]xerj.Hit, error)
}

// WithIndex gives the planner an index to search, which offers the search_index tool. Nil, the
// default, leaves the tool out, so a model never calls a tool whose server is not there.
func (p *Planner) WithIndex(s IndexSearcher) *Planner {
	p.index = s
	return p
}

// WithXerj offers search_index against the node the xerj block names, when the block is on. The
// settings are passed one by one because this package does not read the configuration.
func (p *Planner) WithXerj(enabled bool, url, apiKey, index string) *Planner {
	if !enabled {
		return p
	}
	return p.WithIndex(xerj.New(url, apiKey, index))
}

// searchIndexArgs are the arguments for search_index.
type searchIndexArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// searchIndexTool describes search_index to the model.
func searchIndexTool() llm.Tool {
	return llm.NewTool("search_index", "Searches an indexed folder (a local XERJ index) by what the code or text "+
		"is about or by a symbol name, and returns the best matching passages as path:line plus a few lines, "+
		"definitions first. Cheaper than search_in_files for a question in plain words; it only knows what was "+
		"indexed, so confirm a negative with search_in_files.",
		llm.ObjectSchema(map[string]any{
			"query": llm.StringProperty("Plain words or a symbol name (for example \"where the WAL segment rotates\")."),
			"limit": llm.IntegerProperty("Maximum number of passages (default 5, at most 20)."),
		}, "query"))
}

// toolSearchIndex asks the index. A node that is not there is an answer the model can act on
// (fall back to search_in_files), not a failure of the turn.
func (p *Planner) toolSearchIndex(ctx context.Context, raw []byte) string {
	var args searchIndexArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "Error: the 'query' parameter is missing."
	}
	hits, err := p.index.Search(ctx, query, args.Limit)
	if err != nil {
		return "Error searching the index: " + err.Error()
	}
	return limitString(xerj.Format(query, hits), maxToolOutputBytes)
}
