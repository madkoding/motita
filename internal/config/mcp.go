package config

import (
	"fmt"
	"strings"
)

// MCPServer is a Model Context Protocol server the person connected (agent.mcp). It is a program
// motita starts and talks to over its standard input and output; what it offers reaches the model
// as the actions mcp_tools and mcp_call, and every call is asked about first.
type MCPServer struct {
	// Name is how the model and the person refer to the server. Letters, digits, - and _.
	Name string `yaml:"name"`
	// Command and Args start the server.
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	// Env is added to a minimal environment (PATH and HOME): the person's own variables are not
	// handed to a program they only connected.
	Env map[string]string `yaml:"env"`
	// Dir is the directory the server starts in; empty is the workspace.
	Dir string `yaml:"dir"`
	// Skills lets the model read the skills this server serves (instructions for using its tools
	// well). They are written by whoever runs the server, so they are read only from a server the
	// person marked here, and are never run.
	Skills bool `yaml:"skills"`
}

// validateMCP checks the servers: a name each, unique, and a command to start.
func (a *Agent) validateMCP() error {
	seen := map[string]bool{}
	for i, s := range a.MCP {
		path := fmt.Sprintf("agent.mcp[%d]", i)
		if s.Name == "" || strings.Trim(s.Name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
			return fmt.Errorf("%s.name %q must be letters, digits, - and _", path, s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("%s.name %q is used twice", path, s.Name)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("%s.command is empty: say which program starts the server", path)
		}
	}
	return nil
}
