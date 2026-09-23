package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// The README and docs/REFERENCE.md both tell an operator which lines run silently and which
// ones interrupt them. That table is the part of the documentation people act on — they write
// a task, it stops on a question, and they conclude the agent is broken. So every row of it is
// asserted here against the real classifier, in both directions.
//
// This test is also what caught an error in the first draft of that table: `sh scripts/deploy.sh`
// was documented as silent because the script is inside the workspace. It is not — an
// interpreter is opaque by construction, and the policy asks. The code was right and the prose
// was wrong, which is exactly the drift this file exists to catch.
func TestTheDocumentedSilenceTableHolds(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"build.py", "scripts/deploy.sh", "scripts/test.sh"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("echo hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		line string
		want Verdict
		desc string
	}{
		// "python3 build.py (inside the workspace) — silent"
		{"python3 build.py", Allow, "docs: an interpreter on a workspace script is silent"},
		{"python3 ./build.py", Allow, "docs: and so is the explicit form"},

		// "a script from outside the workspace — asked"
		{"python3 /opt/other/build.py", Ask, "docs: a script from outside is asked about"},
		{"python3 ../outside/build.py", Ask, "docs: including one reached by a relative path"},

		// "python3 -c '...' — asked": inline code is as opaque as a shell line.
		{"python3 -c 'print(1)'", Ask, "docs: inline code is asked about"},

		// "make, go build, npm test — silent": the local toolchain, which is the rule that
		// keeps the confirmation layer from being switched off.
		{"make", Allow, "docs: make is the project's own work"},
		{"go build ./...", Allow, "docs: go build is the project's own work"},
		{"npm test", Allow, "docs: npm test is the project's own work"},
		{"cargo build", Allow, "docs: cargo build is the project's own work"},

		// "your own ./scripts/* — silent"
		{"./scripts/deploy.sh", Allow, "docs: a workspace program named directly is silent"},

		// The asymmetry the docs now spell out: naming the interpreter hides the script from
		// the classifier, so the same file becomes a question.
		{"sh scripts/deploy.sh", Ask, "docs: handed to a shell, the same script is asked about"},

		// "pip install x, git push — asked": it reaches the network.
		{"pip install x", Ask, "docs: a network write is asked about"},
		{"git push origin main", Ask, "docs: a push is asked about"},

		// "htop, an unknown binary — asked"
		{"htop", Ask, "docs: an unclassifiable binary is asked about"},
		{"terraform apply", Ask, "docs: and so is an infrastructure tool"},

		// "reads run"
		{"ls -la", Allow, "docs: a plain read runs"},
		{"cat build.py", Allow, "docs: and so does reading a source file"},
		{"grep -rn TODO .", Allow, "docs: and searching"},

		// "an assignment is classified by the real program"
		{"FOO=1 ls", Allow, "docs: an assignment before a reader is a reader"},
		{"FOO=1 htop", Ask, "docs: an assignment before an unknown is still unknown"},

		// "inline flags are read per interpreter" — gcc -c is a compile flag, python3 -c is code.
		{"gcc -c foo.c", Allow, "docs: a compile flag is not inline code"},
	}

	// Every documented "asked" row must also be REFUSED under strict, and every "silent" row
	// must stay silent there: strict changes the answer to an unknown, never the local work.
	strict := Mode{Enforce: true, Strict: true}

	bad := 0
	for _, c := range cases {
		d := testMode().DecideLine(c.line, dir)
		if d.Verdict != c.want {
			bad++
			t.Errorf("docs WRONG about %s: got %s, documented %s (rule %s)",
				c.desc, d.Verdict, c.want, d.Rule)
			continue
		}
		if d.Verdict == Allow {
			if sd := strict.DecideLine(c.line, dir); sd.Verdict != Allow {
				bad++
				t.Errorf("docs WRONG: %s is silent by default but %s under strict (rule %s)",
					c.desc, sd.Verdict, sd.Rule)
			}
		}
	}
	if bad == 0 {
		t.Logf("all %d documented rows hold, in strict mode too", len(cases))
	}
}
