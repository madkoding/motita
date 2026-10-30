package plan

import (
	"context"
	"encoding/json"

	"github.com/madkoding/motita/internal/knowledge"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/memory"
)

// The two tools over everything the agent knows (see internal/knowledge). They replace the four
// memory tools and the four skill tools, which stay answered under their old names for a model or
// a saved procedure that still calls them, but are no longer offered.

// knowledgeArgs are the arguments of recall and remember.
type knowledgeArgs struct {
	In      string `json:"in"`
	Query   string `json:"query"`
	Open    string `json:"open"`
	Entry   string `json:"entry"`
	Content string `json:"content"`
}

// placeProperty describes the "in" argument both tools take.
func placeProperty() map[string]any {
	return llm.StringProperty("Where: memory (the person's facts, decisions and plans), procedures (how to do a " +
		"kind of work), project (lessons about this project) or conversation (earlier turns of this chat). " +
		"Leave it out to look everywhere.")
}

// knowledgeTools describes recall and remember.
func knowledgeTools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("recall", "Looks up what is already known, by words and meaning: earlier turns of this "+
			"conversation, this project's lessons, the person's memory and the procedures for kinds of work. "+
			"Use it before working something out again or asking the person. Give query to search, or open "+
			"to read one entry or procedure in full; cite memory entries as [[Title]].",
			llm.ObjectSchema(map[string]any{
				"query": llm.StringProperty("The words to look for. In the memory, author:Name since:2026-01-31 " +
					"until:2026-12-31 ns:Collection graph:1 narrow it."),
				"open": llm.StringProperty("A memory entry's title or path, or a procedure's name, to read in full."),
				"in":   placeProperty(),
			})),
		llm.NewTool("remember", "Keeps what a later turn or session will need: in memory a decision, preference "+
			"or fact the person stated (added to an entry, never replacing it); in procedures how to do a kind "+
			"of work (the steps, the pitfalls and why, never a diary); in project a fact about this project. "+
			"Never secrets.",
			llm.ObjectSchema(map[string]any{
				"entry":   llm.StringProperty("The memory entry's title or path, or the procedure's name (lowercase, hyphenated)."),
				"content": llm.StringProperty("The Markdown to keep."),
				"in":      placeProperty(),
			}, "content")),
	}
}

// knowledgeBase is what this planner can reach.
func (p *Planner) knowledgeBase(ctx context.Context) knowledge.Base {
	b := knowledge.Base{Memory: p.memory, Workspace: p.workspace}
	if p.library != nil {
		b.Library = plannerLibrary{p}
	}
	if p.recall != nil {
		b.Conversation = func(query string) string {
			block, _ := p.recall(ctx, query, 0)
			return block
		}
	}
	return b
}

// toolKnowledge runs recall or remember.
func (p *Planner) toolKnowledge(ctx context.Context, name string, raw []byte) string {
	var args knowledgeArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	b := p.knowledgeBase(ctx)
	if name == "recall" {
		return limitString(b.Recall(args.In, args.Query, args.Open), maxToolOutputBytes)
	}
	reply := b.Remember(args.In, args.Entry, args.Content)
	if line, ok := memory.SavedLine(reply); ok {
		p.tracef("%s", line)
	}
	return reply
}

// plannerLibrary is the procedure library as this planner reaches it, with its bookkeeping.
type plannerLibrary struct{ p *Planner }

func (l plannerLibrary) Search(query string) string {
	return l.p.toolSearchSkills(jsonArgs(map[string]string{"query": query}))
}

func (l plannerLibrary) Read(name string) string {
	return l.p.toolReadSkill(jsonArgs(map[string]string{"name": name}))
}

func (l plannerLibrary) Save(name, body string) string {
	return l.p.toolSaveSkill(jsonArgs(map[string]string{"name": name, "body": body}))
}

// jsonArgs encodes tool arguments.
func jsonArgs(v map[string]string) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
