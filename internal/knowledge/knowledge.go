// Package knowledge is the one door the agent has to what it knows.
//
// What is known lives in four places, each kept where it belongs: the person's memory (a folder of
// Markdown, see internal/memory), the procedure library (see internal/skills), the lessons earlier
// tasks learned about this project (LessonsFile, inside the project) and the archive of this
// conversation (see internal/contextmem). The model used to reach them through eight tools with two
// spellings, one set per mode, plus a lessons file it could not search; it now reaches all of them
// through two, recall and remember, in both modes. The files stay where they were, so nothing is
// migrated and nothing can be lost: only the door changed.
package knowledge

import (
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/memory"
	"github.com/madkoding/motita/internal/redact"
)

// The places, by the names the model uses in "in:".
const (
	Memory       = "memory"
	Procedures   = "procedures"
	Project      = "project"
	Conversation = "conversation"
)

// sectionChars bounds what one place adds to a recall over all of them.
const sectionChars = 2500

// Library is the procedure library as each mode reaches it, with its own bookkeeping (what was
// read, its value, its complaints). Its answers are the library's own words.
type Library interface {
	Search(query string) string
	Read(name string) string
	Save(name, body string) string
}

// Base is what one session knows. A nil or empty field is a place this session does not have.
type Base struct {
	Memory  *memory.Store
	Library Library
	// Workspace is the project whose lessons are read and written.
	Workspace string
	// Conversation searches the archive of this conversation; it answers "" when nothing matches.
	Conversation func(query string) string
}

// aliases are the other names a model gives a place.
var aliases = map[string]string{
	"": "", "all": "",
	Memory: Memory, "memoria": Memory, "person": Memory, "persona": Memory, "notes": Memory,
	Procedures: Procedures, "procedure": Procedures, "procedimientos": Procedures, "skills": Procedures,
	"skill": Procedures, "library": Procedures,
	Project: Project, "proyecto": Project, "lessons": Project, "lecciones": Project,
	Conversation: Conversation, "conversacion": Conversation, "conversación": Conversation, "chat": Conversation,
}

// place reads the name of a place, or reports the ones that exist.
func place(in string) (string, error) {
	p, ok := aliases[strings.ToLower(strings.TrimSpace(in))]
	if !ok {
		return "", fmt.Errorf("there is no place called %q: use memory, procedures, project or conversation, "+
			"or leave it out to look everywhere", in)
	}
	return p, nil
}

// oldNames are the tools this door replaced, as the places' own answers still name them.
var oldNames = strings.NewReplacer(
	"search_memory", "recall", "memory_search", "recall",
	"read_memory", "recall (open)", "memory_read", "recall (open)",
	"append_memory", "remember", "memory_append", "remember",
	"write_memory", "remember", "memory_write", "remember",
	"list_skills", "recall in:procedures", "search_skills", "recall in:procedures",
	"read_skill", "recall in:procedures (open)", "save_skill", "remember in:procedures",
)

// Recall answers a search (query) or opens one entry (open: a memory entry's title or path, or a
// procedure's name). With no place it looks everywhere and says where each answer comes from.
func (b Base) Recall(in, query, open string) string {
	where, err := place(in)
	if err != nil {
		return "Error: " + err.Error()
	}
	if open = strings.TrimSpace(open); open != "" {
		return oldNames.Replace(b.open(where, open))
	}
	if strings.TrimSpace(query) == "" {
		return "Error: say what to look for, or name the entry to open."
	}
	if where != "" {
		return oldNames.Replace(b.search(where, query, false))
	}
	var out strings.Builder
	for _, p := range []string{Conversation, Project, Memory, Procedures} {
		if found := b.search(p, query, true); found != "" {
			fmt.Fprintf(&out, "## %s\n%s\n\n", heading[p], cut(found, sectionChars))
		}
	}
	if out.Len() == 0 {
		return fmt.Sprintf("Nothing is known about %q yet: not in this conversation, this project's lessons, "+
			"the person's memory or the procedures. Work it out, and remember what is worth keeping.", query)
	}
	return oldNames.Replace(strings.TrimSpace(out.String()))
}

// heading names each place in a recall over all of them.
var heading = map[string]string{
	Conversation: "Earlier in this conversation",
	Project:      "This project's lessons",
	Memory:       "The person's memory",
	Procedures:   "Procedures",
}

// search asks one place. quiet turns "nothing here" into "", for a recall over every place.
func (b Base) search(where, query string, quiet bool) string {
	var found string
	switch where {
	case Conversation:
		if b.Conversation != nil {
			found = b.Conversation(query)
		}
	case Project:
		found = matchingLessons(b.Workspace, query)
	case Memory:
		if b.Memory != nil {
			found = b.Memory.SearchText(query)
			if strings.HasPrefix(found, "Nothing in the memory matches") && quiet {
				found = ""
			}
		}
	default: // Procedures
		if b.Library != nil {
			found = b.Library.Search(query)
			if strings.HasPrefix(found, "No skill matches") && quiet {
				found = ""
			}
		}
	}
	if found == "" && !quiet {
		return fmt.Sprintf("Nothing in %s matches %q.", where, query)
	}
	return found
}

// open reads one entry. With no place, a memory entry is tried first, then a procedure.
func (b Base) open(where, name string) string {
	switch where {
	case Project:
		if all := LessonsContext(b.Workspace); all != "" {
			return strings.TrimSpace(all)
		}
		return "This project has no lessons yet."
	case Conversation:
		return b.search(Conversation, name, false)
	case Procedures:
		return b.readProcedure(name)
	default:
		if b.Memory != nil {
			read := b.Memory.ReadText(name)
			if where == Memory || !strings.HasPrefix(read, "Error: there is no entry") {
				return read
			}
		} else if where == Memory {
			return "Error: there is no memory in this session."
		}
		return b.readProcedure(name)
	}
}

// readProcedure reads a procedure by name.
func (b Base) readProcedure(name string) string {
	if b.Library == nil {
		return fmt.Sprintf("Error: nothing called %q, and there is no procedure library in this session.", name)
	}
	return b.Library.Read(name)
}

// Remember keeps what is worth keeping. In the memory it adds to an entry (creating it), never
// replacing what the person wrote; in the procedures it writes the procedure called entry; in the
// project it adds each line of content as a lesson.
func (b Base) Remember(in, entry, content string) string {
	where, err := place(in)
	if err != nil {
		return "Error: " + err.Error()
	}
	content = redact.String(strings.TrimSpace(content))
	if content == "" {
		return "Error: say what to remember."
	}
	switch where {
	case Conversation:
		return "Error: this conversation keeps itself. Remember a decision in the memory, or a fact about " +
			"the project in project."
	case Project:
		lines := strings.Split(content, "\n")
		if err := RecordLessons(b.Workspace, lines); err != nil || b.Workspace == "" {
			return "Error: this session has no project to keep lessons in."
		}
		return fmt.Sprintf("Kept as this project's lessons (%s); every later task reads them.", LessonsFile)
	case Procedures:
		if b.Library == nil {
			return "Error: there is no procedure library in this session."
		}
		return oldNames.Replace(b.Library.Save(strings.TrimSpace(entry), content))
	default: // Memory
		if b.Memory == nil {
			return "Error: there is no memory in this session."
		}
		return oldNames.Replace(b.Memory.AppendText(strings.TrimSpace(entry), content))
	}
}

// matchingLessons are the project's lessons that share a word with the query.
func matchingLessons(workspace, query string) string {
	words := strings.Fields(strings.ToLower(query))
	var out []string
	for _, l := range Lessons(workspace) {
		low := strings.ToLower(l)
		for _, w := range words {
			if len([]rune(w)) > 3 && strings.Contains(low, w) {
				out = append(out, "- "+l)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// cut bounds one place's answer in a recall over all of them.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && s[end]&0xC0 == 0x80 {
		end--
	}
	return s[:end] + "\n[…more: recall in this place alone]"
}
