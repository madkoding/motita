package policy

import "testing"

// TestFewerQuestionsForWhatIsAlreadyAllowed: a workspace script handed to a shell, an in-place
// sed edit of a workspace file and `git fetch` run without a question, while the forms of the
// same programs that do more are still asked about.
func TestFewerQuestionsForWhatIsAlreadyAllowed(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		line string
		want Verdict
		rule string
	}{
		{"bash scripts/check.sh", Allow, "project-script"},
		{"sh -ex ./run.sh arg", Allow, "project-script"},
		{"zsh -eu run.sh", Allow, "project-script"},
		{"bash scripts/check.sh /etc/hosts", Ask, "project-script-arg-outside"},
		{"bash ../elsewhere.sh", Ask, "shell"},
		{"bash -c 'ls'", Ask, "shell"},
		{"fish run.fish", Ask, "shell"},
		{"sed -i 's/a/b/' main.go", Allow, "write-inside-workspace"},
		{"sed -i 's/a/b/' /etc/hosts", Ask, "write-outside-workspace"},
		{"sed -i 's/a/b/w /tmp/x' main.go", Ask, "writer-unclassified"},
		{"git fetch origin", Allow, "git-fetch"},
		{"git fetch --prune", Allow, "git-fetch"},
		{"git fetch origin main:main", Ask, categoryExternalEffect},
		{"git fetch --upload-pack=x origin", Ask, categoryExternalEffect},
		{"git pull", Ask, categoryExternalEffect},
		{"xargs -0 -n 5 -P4 grep -l TODO", Allow, "xargs-reader"},
		{"xargs -r wc -l", Allow, "xargs-reader"},
		{"xargs rm", Ask, "unclassified"},
		{"xargs sort", Ask, "unclassified"},
		{"xargs --max-args=2 grep x", Ask, "unclassified"},
		{"xargs -r", Ask, "unclassified"},
		{"xargs -n", Ask, "unclassified"},
		{"xargs grep -r x .", Allow, "xargs-reader"},
		{"export CI=1 GOFLAGS", Allow, "export"},
		{"export PATH=/tmp/bin", Ask, "unclassified"},
		{"export LD_PRELOAD=x.so", Ask, "unclassified"},
		{"export DYLD_INSERT_LIBRARIES=x", Ask, "unclassified"},
		{"export -n X", Ask, "unclassified"},
	}
	for _, c := range cases {
		d := testMode().DecideLine(c.line, dir)
		if d.Verdict != c.want || d.Rule != c.rule {
			t.Errorf("%q: got %s (%s), want %s (%s): %s", c.line, d.Verdict, d.Rule, c.want, c.rule, d.Reason)
		}
	}
}
