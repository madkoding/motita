package policy

import (
	"slices"
	"strings"
	"testing"
)

func TestCommandsSplitsALineIntoItsCommands(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"git push origin main", []string{"git push origin main"}},
		{`git commit -m "a && b" && git push`, []string{`git commit -m "a && b"`, "git push"}},
		{"ls | wc -l; echo done > out.txt", []string{"ls", "wc -l", "echo done"}},
		{"cat > f << 'EOF'\nrm -rf /\nEOF", []string{"cat"}},
		{"for f in a b; do rm $f; done", []string{"rm $f"}},
		{"", nil},
	} {
		var got []string
		for _, words := range Commands(tc.line) {
			got = append(got, strings.Join(words, " "))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("Commands(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestIsBuiltinNamesTheWordsTheShellRunsItself(t *testing.T) {
	if !IsBuiltin("cd") || !IsBuiltin("echo") || IsBuiltin("git") {
		t.Error("cd and echo are builtins, git is a program")
	}
}
