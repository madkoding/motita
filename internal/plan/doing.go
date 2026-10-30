package plan

import (
	"encoding/json"
	"strings"
)

// doingWidth keeps a description to one line.
const doingWidth = 90

// toolNames is what a tool call is called when the model did not describe it.
var toolNames = map[string]string{
	"list_directory":  "Listing a folder",
	"read_file":       "Reading a file",
	"execute_command": "Running a command",
	"search_in_files": "Searching the code",
	"list_skills":     "Looking at the skills",
	"search_skills":   "Searching the skills",
	"read_skill":      "Reading a skill",
	"save_skill":      "Saving a skill",
	"search_memory":   "Searching the memory",
	"read_memory":     "Reading the memory",
	"write_memory":    "Writing to the memory",
	"append_memory":   "Writing to the memory",
	"fetch_url":       "Reading a web page",
	"recall":          "Looking up what is known",
	"remember":        "Keeping what was learned",
}

// toolDoing says in plain words what a tool call is for: the "description" the model gave it, or the
// name of the tool.
func toolDoing(name string, args json.RawMessage) string {
	var a struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(args, &a)
	if d := strings.Join(strings.Fields(a.Description), " "); d != "" {
		if r := []rune(d); len(r) > doingWidth {
			d = string(r[:doingWidth]) + "..."
		}
		return d
	}
	if d, ok := toolNames[name]; ok {
		return d
	}
	return "Running an action"
}
