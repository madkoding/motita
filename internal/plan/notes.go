package plan

import (
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// WithNotes gives the planner the note vault, which offers the note tools. Nil, the default, leaves
// them out.
func (p *Planner) WithNotes(v *notes.Vault) *Planner {
	p.notes = v
	return p
}

// noteArgs are the arguments of the note tools.
type noteArgs struct {
	Query   string `json:"query"`
	Note    string `json:"note"`
	Content string `json:"content"`
}

// noteTools describes the tools over the person's notes.
func noteTools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("search_notes", "Searches the person's notes (their second brain: plain Markdown) by the words "+
			"they use, and returns the matching notes as [[Title]] with their path and line. Use it before "+
			"answering anything about the person's own facts, decisions, plans or projects.",
			llm.ObjectSchema(map[string]any{
				"query": llm.StringProperty("The words to look for."),
			}, "query")),
		llm.NewTool("read_note", "Reads one note in full, by title or path, with the notes it links to and the "+
			"notes that link to it. Cite what you take from it as [[Title]].",
			llm.ObjectSchema(map[string]any{
				"note": llm.StringProperty("The note's title or path, as search_notes returned it."),
			}, "note")),
		llm.NewTool("write_note", "Creates a new note (never replaces one). Link related notes with [[Title]].",
			llm.ObjectSchema(map[string]any{
				"note":    llm.StringProperty("Title or path of the new note, for example \"projects/Trip\"."),
				"content": llm.StringProperty("The note's Markdown."),
			}, "note", "content")),
		llm.NewTool("append_note", "Adds text to the end of a note (creating it when it does not exist). "+
			"This is how to record a decision or a fact without losing what the person wrote.",
			llm.ObjectSchema(map[string]any{
				"note":    llm.StringProperty("The note's title or path."),
				"content": llm.StringProperty("The Markdown to add."),
			}, "note", "content")),
	}
}

// toolNote runs one of the note tools.
func (p *Planner) toolNote(name string, raw []byte) string {
	var args noteArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	switch name {
	case "search_notes":
		return limitString(p.notes.SearchText(args.Query), maxToolOutputBytes)
	case "read_note":
		return limitString(p.notes.ReadText(args.Note), maxToolOutputBytes)
	case "write_note":
		return p.notes.WriteText(args.Note, redact.String(args.Content))
	default: // append_note
		return p.notes.AppendText(args.Note, redact.String(args.Content))
	}
}
