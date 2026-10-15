package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// readFileArgs are the arguments for read_file.
type readFileArgs struct {
	Path string `json:"path"`
}

// listDirectoryArgs are the arguments for list_directory.
type listDirectoryArgs struct {
	Path string `json:"path"`
}

// toolListDirectory lists a directory's entries.
func (p *Planner) toolListDirectory(raw []byte) string {
	var args listDirectoryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	path, err := p.readPath(args.Path)
	if err != nil {
		return errPrefix + err.Error()
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "Error listing the directory: " + err.Error()
	}
	if len(entries) == 0 {
		return "(empty directory)"
	}
	var sb strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		fmt.Fprintln(&sb, name)
	}
	return strings.TrimSpace(sb.String())
}

// toolReadFile reads a text file with the 1 MiB limit.
func (p *Planner) toolReadFile(raw []byte) string {
	var args readFileArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	if strings.TrimSpace(args.Path) == "" {
		return "Error: the 'path' parameter is missing."
	}
	path, err := p.readPath(args.Path)
	if err != nil {
		return errPrefix + err.Error()
	}

	fi, err := os.Stat(path)
	if err != nil {
		return "Error accessing the file: " + err.Error()
	}
	if fi.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; list its contents with execute_command.", path)
	}
	if fi.Size() > maxFileBytes {
		return fmt.Sprintf("Error: the file is %d bytes and exceeds the %d-byte limit (1 MiB).", fi.Size(), maxFileBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return "Error opening the file: " + err.Error()
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		return "Error reading the file: " + err.Error()
	}
	if len(data) == 0 {
		return "(the file is empty)"
	}
	return string(data)
}

// PathResolver is the optional half of a CommandRunner that knows the session's working directory:
// it resolves a path a read tool was given against it and refuses one the session may not read.
// The real *agent.Agent implements it. read_file, list_directory and the Go search run in THIS
// process, not as a sandboxed command, so without it they would resolve a relative path against
// the gateway's own directory and read anywhere the gateway can.
type PathResolver interface {
	ReadPath(path string) (string, error)
}

// readPath resolves a read tool's path through the runner when it can, and keeps it as given
// otherwise (a planner built without a runner that knows a workspace, as in the tests).
func (p *Planner) readPath(path string) (string, error) {
	if r, ok := p.runner.(PathResolver); ok {
		return r.ReadPath(path)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	return path, nil
}

// executeCommandArgs are the arguments for execute_command.
type executeCommandArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// toolExecuteCommand runs a command through the agent's read-only executor.
func (p *Planner) toolExecuteCommand(ctx context.Context, raw []byte) string {
	var args executeCommandArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return "Error: the 'command' parameter is missing."
	}

	timeout := p.commandTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, exit, err := p.runner.RunCommand(toolCtx, command)
	output = limitString(output, maxToolOutputBytes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "$ %s\n", command)
	if err != nil {
		fmt.Fprintf(&sb, "Execution error: %v\n", err)
	}
	if output != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "[exit=%d]", exit)
	return sb.String()
}

// searchInFilesArgs are the arguments for search_in_files.
type searchInFilesArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Literal string `json:"literal"`
}

// toolSearchInFiles searches text files for a pattern.
func (p *Planner) toolSearchInFiles(ctx context.Context, raw []byte) string {
	var args searchInFilesArgs
	if err := decodeArgs(raw, &args); err != nil {
		return errParsingArguments + err.Error()
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "Error: the 'pattern' parameter is missing."
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		path = "."
	}
	// The path is checked here for both searches. grep keeps the path as given, since it runs in
	// the working directory and its output reads better relative; the Go search takes the resolved
	// one, because it runs in this process, whose directory is not the session's.
	resolved, err := p.readPath(path)
	if err != nil {
		return errPrefix + err.Error()
	}

	toolCtx, cancel := context.WithTimeout(ctx, p.commandTimeout)
	defer cancel()

	literal := strings.EqualFold(args.Literal, "true")
	var command, output string
	var exit int
	if searchNatively {
		command = fmt.Sprintf("search_in_files %s %s", shellQuote(pattern), shellQuote(path))
		output, exit, err = searchFiles(toolCtx, pattern, resolved, literal)
	} else {
		if literal {
			command = fmt.Sprintf("grep -R -n -F %s %s", shellQuote(pattern), shellQuote(path))
		} else {
			command = fmt.Sprintf("grep -R -n -E %s %s", shellQuote(pattern), shellQuote(path))
		}
		output, exit, err = p.runner.RunCommand(toolCtx, command)
	}
	output = limitString(output, maxToolOutputBytes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "$ %s\n", command)
	if err != nil {
		fmt.Fprintf(&sb, "Execution error: %v\n", err)
	}
	if output != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "[exit=%d]", exit)
	return sb.String()
}

// decodeArgs normalizes provider differences (object, stringified object, empty).
func decodeArgs(raw []byte, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		trimmed = []byte(strings.TrimSpace(text))
	}
	if len(trimmed) == 0 {
		return nil
	}
	return json.Unmarshal(trimmed, dst)
}

// limitString truncates text to the given byte length and appends a notice.
func limitString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n[... output truncated to %d KiB ...]", max/1024)
}
