package app

import (
	"fmt"

	"github.com/madkoding/motita/internal/codeindex"
)

// indexActions is the list every rejection of `motita index` prints.
const indexActions = "status or clear"

// localCommand says the invocation is a subcommand that only touches this machine: the AppArmor
// profile or the code index.
func (b flags) localCommand() bool { return b.sandboxAction != "" || b.indexAction != "" }

// runLocalCommand dispatches the subcommands localCommand names.
func (op Options) runLocalCommand(fl flags) int {
	if fl.indexAction != "" {
		return op.runIndexCommand(fl)
	}
	return op.runSandboxCommand(fl.sandboxAction)
}

// runIndexCommand is `motita index <status|clear>`: the code index kept in code_index.cache_dir.
// Like the other subcommands it builds neither an engine nor a sandbox, and it needs no LLM key.
func (op Options) runIndexCommand(fl flags) int {
	cfg, code := op.curatorConfig(fl)
	if code != Success {
		return code
	}
	dir := cfg.CodeIndex.CacheDir
	if dir == "" {
		fmt.Fprintln(op.Out, "The code index is kept in memory only: code_index.cache_dir is empty.")
		return Success
	}
	if fl.indexAction == "clear" {
		files, size, err := codeindex.ClearCache(dir)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ could not clear %s: %v\n", dir, err)
			return RunError
		}
		fmt.Fprintf(op.Out, "✅ %d index files deleted from %s (%d KB). The next search builds them again.\n",
			files, dir, (size+1023)/1024)
		return Success
	}
	files, size := codeindex.CacheSize(dir)
	fmt.Fprintf(op.Out, "%s: %d index files, %d KB\n", dir, files, (size+1023)/1024)
	return Success
}
