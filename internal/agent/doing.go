package agent

import (
	"path"
	"strings"
)

// DoingPrefix marks the line that says, in plain words, what the action about to run is for
// ("Reading the folder's files"). It comes before the action's own "running:" line, which carries
// the command: an interface shows the words in the conversation and keeps the command in its
// actions tab, so the reader sees what is being done instead of how.
const DoingPrefix = "doing: "

// doingWidth keeps the description to one line.
const doingWidth = 90

// kindDoing is what an action of each kind is called when the model did not describe it.
var kindDoing = map[string]string{
	"write_file":    "Writing a file",
	"edit_file":     "Editing a file",
	"spawn_agent":   "Starting a background agent",
	"wait_agents":   "Waiting for the background agents",
	"list_skills":   "Looking at the skills",
	"search_skills": "Searching the skills",
	"read_skill":    "Reading a skill",
	"save_skill":    "Saving a skill",
	"thread":        "Looking at the conversation",
	"memory_search": "Searching the memory",
	"memory_read":   "Reading the memory",
	"memory_write":  "Writing to the memory",
	"memory_append": "Writing to the memory",
	"box_up":        "Starting a service",
	"box_get":       "Reading a service",
	"box_shot":      "Taking a picture of a service",
	"box_down":      "Stopping a service",
}

// programDoing is what a shell command is called when the model did not describe it, by the program
// it runs.
var programDoing = map[string]string{
	"ls": "Listing files", "tree": "Listing files", "find": "Listing files", "fd": "Listing files",
	"cat": "Reading a file", "head": "Reading a file", "tail": "Reading a file", "sed": "Reading a file",
	"less": "Reading a file", "bat": "Reading a file", "nl": "Reading a file", "wc": "Reading a file",
	"grep": "Searching the code", "rg": "Searching the code", "ag": "Searching the code",
	"git": "Using git", "gh": "Using GitHub",
	"mkdir": "Changing files", "touch": "Changing files", "cp": "Changing files", "mv": "Changing files",
	"rm": "Changing files", "chmod": "Changing files",
	"curl": "Fetching a page", "wget": "Fetching a page",
	"pwd": "Looking around", "echo": "Looking around", "which": "Looking around", "env": "Looking around",
}

// doing is the words that say what an action is for: the model's own description when it gave one,
// otherwise a name derived from the kind of action and, for a shell command, the program it runs.
// It is always one short line and never empty.
func (c Command) doing() string {
	if d := truncate(collapse(c.Description), doingWidth); d != "" {
		return d
	}
	return DescribeAction(c.Kind, c.Command)
}

// DescribeAction names an action that came without a description.
func DescribeAction(kind, command string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if d, ok := kindDoing[kind]; ok {
		return d
	}
	if strings.HasPrefix(kind, "desktop_") {
		return "Using the desktop"
	}
	if kind != "" && kind != "command" {
		return "Running an action"
	}
	return describeCommand(command)
}

// describeCommand names a shell command by the program it runs.
func describeCommand(command string) string {
	program, rest := programOf(command)
	if d, ok := programDoing[program]; ok {
		return d
	}
	switch program {
	case "go", "npm", "npx", "yarn", "pnpm", "cargo", "make", "pytest", "python", "python3", "mvn", "gradle":
		return toolDoing(program, rest)
	}
	return "Running a command"
}

// toolDoing names a build tool's command: its tests, its build or just a run.
func toolDoing(program, rest string) string {
	words := " " + strings.ToLower(rest) + " "
	switch {
	case program == "pytest" || strings.Contains(words, " test ") || strings.Contains(words, " tests "):
		return "Running the tests"
	case strings.Contains(words, " build ") || strings.Contains(words, " vet ") || strings.Contains(words, " lint "):
		return "Building and checking the project"
	case strings.Contains(words, " install ") || strings.Contains(words, " get ") || strings.Contains(words, " add "):
		return "Installing dependencies"
	}
	return "Running a command"
}

// programOf is the program a command line runs and the words after it: the first command of the
// line, past any `cd dir &&`, environment assignments and path.
func programOf(command string) (string, string) {
	line, _, _ := strings.Cut(strings.TrimSpace(command), "\n")
	for _, sep := range []string{"&&", ";"} {
		for strings.HasPrefix(strings.TrimSpace(line), "cd ") && strings.Contains(line, sep) {
			_, after, _ := strings.Cut(line, sep)
			line = after
		}
	}
	words := strings.Fields(line)
	for len(words) > 0 && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") {
		words = words[1:]
	}
	if len(words) == 0 {
		return "", ""
	}
	return path.Base(words[0]), strings.Join(words[1:], " ")
}

// SplitDoing reads a progress line that may carry the words of an action: "doing: <words>", with
// the line of the action's own report ("running: <command>") after it when it has one. rest is the
// line without the words, which is the line as it was before they existed.
func SplitDoing(line string) (doing, rest string, ok bool) {
	body, ok := strings.CutPrefix(line, DoingPrefix)
	if !ok {
		return "", line, false
	}
	doing, rest, _ = strings.Cut(body, "\n")
	return doing, rest, true
}
