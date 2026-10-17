package plan

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/llm"
)

// IndexSearcher is what search_index asks: an index of a folder. The real *codeindex.Searcher
// implements it.
type IndexSearcher interface {
	Search(ctx context.Context, root, query string, limit int) ([]codeindex.Hit, error)
}

// WithIndex gives the planner an index to search, which offers the search_index tool. Nil, the
// default, leaves the tool out.
func (p *Planner) WithIndex(s IndexSearcher) *Planner {
	p.index = s
	return p
}

// WithCodeIndex offers search_index over the built-in index when the code_index block is on. The
// settings are passed one by one because this package does not read the configuration. The index
// is shared between planners, which are built again for every turn.
func (p *Planner) WithCodeIndex(enabled bool, maxFileKB, maxFiles int, cacheDir string, semantic bool) *Planner {
	if !enabled {
		return p
	}
	return p.WithIndex(codeindex.Shared(codeindex.Options{
		MaxFileKB: maxFileKB, MaxFiles: maxFiles, CacheDir: cacheDir, Semantic: semantic,
	}))
}

// searchIndexArgs are the arguments for search_index.
type searchIndexArgs struct {
	Query string `json:"query"`
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

// searchIndexTool describes search_index to the model.
func searchIndexTool() llm.Tool {
	return llm.NewTool("search_index", "Searches the code and text of a folder by what it is about or by a symbol "+
		"name, and returns the best matching passages as path:line plus a few lines, definitions first. Cheaper "+
		"than search_in_files for a question in plain words; it only knows what it indexed (no hidden files, "+
		"dependencies or binaries), so confirm a negative with search_in_files.",
		llm.ObjectSchema(map[string]any{
			"query": llm.StringProperty("Plain words or a symbol name (for example \"where the WAL segment rotates\")."),
			"path": llm.StringProperty("Folder to search in, relative to the working directory. " +
				"Defaults to the working directory."),
			"limit": llm.IntegerProperty("Maximum number of passages (default 5, at most 20)."),
		}, "query"))
}

// toolSearchIndex asks the index, for a folder the session may read.
func (p *Planner) toolSearchIndex(ctx context.Context, raw []byte) string {
	var args searchIndexArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "Error: the 'query' parameter is missing."
	}
	root, err := p.readPath(args.Path)
	if err != nil {
		return errPrefix + err.Error()
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Sprintf("Error: %q is not a directory; search_index searches a folder.", args.Path)
	}
	hits, err := p.index.Search(ctx, root, query, args.Limit)
	if err != nil {
		return "Error searching the index: " + err.Error()
	}
	return limitString(codeindex.Format(query, hits), maxToolOutputBytes)
}
