package plan

import (
	"regexp"
	"strings"

	"github.com/madkoding/motita/internal/llm"
)

// LOOK BEFORE RECOMMENDING TO CHANGE OR DELETE.
//
// Reported from a test of a project: asked for a plan to clean up a session, the agent said a
// repository registered a worktree, and a directory had a Git configuration, without having run a
// single command on either; the repository turned out to be an independent one. A proposal that
// removes or rewrites something rests on how things really are, so an answer that recommends such
// an operation in a turn that never looked at Git or at the path is sent back once to look.

// destructiveProposal is an answer recommending an operation that deletes or rewrites.
var destructiveProposal = regexp.MustCompile(`(?i)(git\s+worktree\s+(remove|prune)|git\s+branch\s+-D|` +
	`git\s+reset\s+--hard|git\s+clean\s+-\S*f|git\s+checkout\s+--\s|git\s+push\s+\S*\s*--force|` +
	`\brm\s+-\S*[rf]|\bRemove-Item\b[^\n]*-Recurse)`)

// lookedAtIt is a command that observes a repository or a path.
var lookedAtIt = regexp.MustCompile(`(?i)(git\s+(-C\s+\S+\s+)?` +
	`(worktree\s+list|rev-parse|status|remote|branch|config|log)|\bls\b|\bstat\b|\bfind\b|\bdu\b|` +
	`\bGet-ChildItem\b|\bTest-Path\b)`)

// lookFirstNote is what a recommendation made without looking is answered with.
const lookFirstNote = "Your answer recommends an operation that deletes or rewrites something (a worktree, a " +
	"branch, a directory, a reset), but nothing in this turn looked at it. Before recommending it, check with " +
	"read-only commands: git worktree list, git -C <path> rev-parse --show-toplevel, git remote -v, ls <path>. " +
	"Then answer again, saying what you checked and what each check showed; do not claim which repository " +
	"registers a worktree, or where a configuration lives, until a command has shown it."

// turnLook remembers whether the turn has looked, and whether it was already sent back.
type turnLook struct {
	looked, sentBack bool
}

// note learns from the tool calls of one round.
func (t *turnLook) note(calls []llm.ToolCall) {
	for _, tc := range calls {
		switch tc.Function.Name {
		case "execute_command":
			if lookedAtIt.Match(tc.Function.Arguments) {
				t.looked = true
			}
		case "list_directory":
			t.looked = true
		default:
			// Every other tool reads a file, a search or a skill: none of them looks at the repository.
		}
	}
}

// sendBack reports whether an answer is to be sent back to look first. It happens at most once per
// turn, so an answer that cannot be checked here still gets delivered.
func (t *turnLook) sendBack(answer string) bool {
	if t.looked || t.sentBack || !destructiveProposal.MatchString(strings.TrimSpace(answer)) {
		return false
	}
	t.sentBack = true
	return true
}
