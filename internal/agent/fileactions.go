package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FILES WRITTEN BY THE PROGRAM, NOT BY THE SHELL.
//
// Reported from a real session: every edit went through `python3 - <<'PY'` scripts that built
// tabs out of chr(9) and newlines out of chr(10), because a literal tab inside the JSON reply
// broke it ("invalid character '\t' in string literal") and a heredoc inside a JSON string is a
// quoting puzzle. Two replies in a row were thrown away for that alone, and every edit after them
// was a small program whose only job was to say "replace this text with that one".
//
// Two action kinds say it directly. The content travels in the "command" field as it is:
//
//	write_file:  first line is the path, everything after the first newline is the whole file.
//	edit_file:   first line is the path, then ONE search/replace block:
//	               <<<<<<< SEARCH
//	               exact text that is in the file now
//	               =======
//	               the text that replaces it
//	               >>>>>>> REPLACE
//
// The markers are the ones models already know from merge conflicts and diff-based editors, so
// they need no teaching, and they do not occur in ordinary source.

const (
	editSearch  = "<<<<<<< SEARCH\n"
	editDivider = "\n=======\n"
	editReplace = "\n>>>>>>> REPLACE"
)

// runFileAction carries out a write_file or edit_file action. It reports whether the kind was
// one of them, what to show the model, and an error only for a REFUSAL (read-only mode, a path
// outside the workspace): those are the run's rules, like a policy refusal. A search that does not
// match is a fact about the file, shown to the model like a command's failing output.
func (a *Agent) runFileAction(kind string, action Command) (handled bool, out string, refusal error) {
	if kind != "write_file" && kind != "edit_file" {
		return false, "", nil
	}
	if a.readOnly() {
		err := errors.New("this session is read-only: files cannot be written")
		return true, "[refused: " + err.Error() + "]", err
	}
	rel, body, _ := strings.Cut(action.Command, "\n")
	path, err := a.workspacePath(rel)
	if err != nil {
		return true, "[refused: " + err.Error() + "]", err
	}
	if kind == "write_file" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return true, fmt.Sprintf("[failed: %v]", err), nil
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return true, fmt.Sprintf("[failed: %v]", err), nil
		}
		return true, fmt.Sprintf("[wrote %s: %d bytes]", rel, len(body)), nil
	}
	return true, editFile(path, rel, body), nil
}

// editFile applies one search/replace block and says what happened. The search text must occur
// EXACTLY ONCE: zero means the model's picture of the file is wrong, more than one means the
// edit is ambiguous, and guessing either way would write the wrong place.
func editFile(path, rel, block string) string {
	block = strings.TrimSuffix(block, "\n")
	if !strings.HasPrefix(block, editSearch) || !strings.HasSuffix(block, editReplace) {
		return "[failed: an edit_file block is the path on the first line, then " +
			strings.TrimSpace(editSearch) + ", the exact current text, =======, the new text, " +
			strings.TrimPrefix(editReplace, "\n") + "]"
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(block, editSearch), editReplace)
	search, replace, ok := strings.Cut(inner, editDivider)
	if !ok {
		// An empty replacement leaves the divider without its trailing newline.
		if s, found := strings.CutSuffix(inner, "\n======="); found {
			search, replace, ok = s, "", true
		}
	}
	if !ok || search == "" {
		return "[failed: the block needs the current text, a ======= line, and the new text]"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("[failed: %v]", err)
	}
	switch n := strings.Count(string(data), search); n {
	case 0:
		return fmt.Sprintf("[failed: the SEARCH text is not in %s. Read the lines you mean to change and "+
			"copy them exactly, indentation included]", rel)
	case 1:
	default:
		return fmt.Sprintf("[failed: the SEARCH text occurs %d times in %s. Include enough surrounding "+
			"lines to make it unique]", n, rel)
	}
	updated := strings.Replace(string(data), search, replace, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("[failed: %v]", err)
	}
	return fmt.Sprintf("[edited %s: 1 replacement]", rel)
}

// firstLineOf is a file action's path, for the log and the progress line: the rest of the command
// is the content, which belongs in neither.
func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// workspacePath resolves a path the model gave against the workspace and refuses one that leaves
// it: these actions write without a shell, so they also write without the shell's policy, and the
// workspace is the boundary that keeps that safe.
func (a *Agent) workspacePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("no path: the first line of the action must be the file's path")
	}
	root, err := filepath.Abs(a.cfg.Agent.WorkspaceDir)
	if err != nil {
		return "", err
	}
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if !within(root, path) {
		return "", fmt.Errorf("%s is outside the working directory %s", rel, root)
	}
	// The lexical check is not the boundary on its own: a symlink inside the workspace (a cloned
	// repository's `notes -> ~/.bashrc`) would carry the write out of it. Both sides are resolved
	// through their symlinks and the check is made again on what the write will really touch.
	realRoot, err := resolveExisting(root)
	if err != nil {
		return "", err
	}
	realPath, err := resolveExisting(path)
	if err != nil || !within(realRoot, realPath) {
		return "", fmt.Errorf("%s resolves outside the working directory %s", rel, root)
	}
	return realPath, nil
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	inside, err := filepath.Rel(root, path)
	return err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))
}

// resolveExisting resolves the symlinks of the deepest part of p that exists and appends the rest,
// which cannot hold a symlink because it does not exist yet. A dangling symlink is an error: where
// it would lead is not known until something creates its target.
func resolveExisting(p string) (string, error) {
	rest := ""
	for {
		if _, err := os.Lstat(p); err == nil || filepath.Dir(p) == p {
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = filepath.Dir(p)
	}
}
