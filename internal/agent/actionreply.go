package agent

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/madkoding/motita/internal/llm"
)

// THE REPLY OF THE ACTION PHASE, and what is done with one that is not the object asked for.
//
// Reported from a real session: 51 of about 130 rounds came back unusable, and each one cost a
// whole round of a long task. Most were not wrong answers, they were right answers in the wrong
// shape: the commands written as pseudo tool calls (`<invoke name="bash">`), or a bare list of
// commands without the object around it. Two things fix that. The provider is asked to enforce
// actionSchema where it can (claude-code does, through --json-schema), so the reply cannot take
// another shape; and where it cannot, a reply whose commands are plain to read is read, instead
// of being sent back to be written again.

// actionSchema is the shape of Action, for the providers that enforce one.
var actionSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "reasoning": {"type": "string"},
    "actions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "kind": {"type": "string"},
          "description": {"type": "string"},
          "command": {"type": "string"}
        },
        "required": ["kind", "command"]
      }
    },
    "final_action": {
      "type": "object",
      "properties": {
        "description": {"type": "string"},
        "command": {"type": "string"}
      }
    },
    "notes": {"type": "string"},
    "done": {"type": "boolean"}
  },
  "required": ["reasoning", "actions", "done"]
}`)

var (
	invokeBlock = regexp.MustCompile(`(?s)<invoke\b[^>]*>(.*?)</invoke>`)
	invokeParam = regexp.MustCompile(`(?s)<parameter\s+name="([^"]*)"\s*>(.*?)</parameter>`)
)

// decodeAction reads the action phase's reply. The object is what is asked for; a list of
// commands, or commands written as pseudo tool calls, are read as a round that is NOT done (the
// model was clearly still working), and anything else is the decoding error as before.
func decodeAction(text string) (Action, error) {
	var action Action
	err := llm.DecodeJSON(text, &action)
	if err == nil {
		return action, nil
	}
	if raw, xerr := llm.ExtractJSON(text); xerr == nil {
		var cmds []Command
		if json.Unmarshal(raw, &cmds) == nil && hasCommand(cmds) {
			return salvaged(cmds), nil
		}
	}
	if cmds := invokeCommands(text); len(cmds) > 0 {
		return salvaged(cmds), nil
	}
	return Action{}, err
}

// salvaged is a round of commands read from a reply in another shape. It is never "done": a
// model that is writing commands is still working.
func salvaged(cmds []Command) Action {
	done := false
	return Action{Reasoning: "(read from a reply that was not the JSON object asked for)", Actions: cmds, Done: &done}
}

// hasCommand reports whether any of the commands has something to run.
func hasCommand(cmds []Command) bool {
	for _, c := range cmds {
		if strings.TrimSpace(c.Command) != "" {
			return true
		}
	}
	return false
}

// invokeCommands reads `<invoke name="..."><parameter name="command">...</parameter></invoke>`
// blocks, the shape a model trained on tool calls falls back to when it is given none. Each block
// with a command is one shell command; one without (`<invoke name="x"></invoke>`) says nothing.
func invokeCommands(text string) []Command {
	var cmds []Command
	for _, block := range invokeBlock.FindAllStringSubmatch(text, -1) {
		params := map[string]string{}
		for _, p := range invokeParam.FindAllStringSubmatch(block[1], -1) {
			params[p[1]] = strings.TrimSpace(p[2])
		}
		if params["command"] == "" {
			continue
		}
		cmds = append(cmds, Command{Kind: "command", Description: params["description"], Command: params["command"]})
	}
	return cmds
}
