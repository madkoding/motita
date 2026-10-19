package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFabricatedChecksAreRecognised is the table of what counts as a stand-in for a check, and what
// does not: the real session wrote vendor/bin/phpunit as `exit(0)` and a composer "test" script
// that called it.
func TestFabricatedChecksAreRecognised(t *testing.T) {
	const phpStub = "<?php\n// fake\nexit(0);\n"
	for name, tc := range map[string]struct {
		kind, path, body string
		refused          bool
	}{
		"a tool made by hand in vendor/bin":  {"write_file", "vendor/bin/phpunit", "#!/usr/bin/env php\nreal();", true},
		"node_modules is installed":          {"write_file", "./node_modules/.bin/jest", "x", true},
		"a virtualenv is installed":          {"write_file", ".venv/bin/pytest", "x", true},
		"windows separators":                 {"write_file", `vendor\bin\phpunit`, "x", true},
		"an executable that only exits 0":    {"write_file", "bin/phpunit", phpStub, true},
		"a shell stand-in":                   {"write_file", "scripts/test.sh", "#!/bin/sh\nset -e\nexit 0\n", true},
		"a node stand-in":                    {"write_file", "scripts/t.js", "process.exit(0)", true},
		"composer test that cannot fail":     {"edit_file", "composer.json", `"test": "exit 0"`, true},
		"composer test as an array":          {"write_file", "composer.json", `{"scripts":{"test":["true"]}}`, true},
		"composer test that only echoes":     {"write_file", "composer.json", `{"scripts":{"test":"echo ok"}}`, true},
		"a php -r exit":                      {"write_file", "composer.json", `{"scripts":{"test":"php -r 'exit(0);'"}}`, true},
		"a Makefile target that cannot fail": {"write_file", "Makefile", "test:\n\t@true\n", true},
		"the real composer script":           {"write_file", "composer.json", `{"scripts":{"test":"php artisan test"}}`, false},
		"a real Makefile target":             {"write_file", "Makefile", "test:\n\tgo test ./...\n", false},
		"an ordinary source file":            {"write_file", "app/Models/User.php", "<?php\nclass User {}\n", false},
		"a script that really runs":          {"write_file", "scripts/test.sh", "#!/bin/sh\nexit $(go test ./... >/dev/null; echo $?)\n", false},
		"vendor as a word in a file name":    {"write_file", "docs/vendors.md", "x", false},
	} {
		got := fakeCheckFile(tc.kind, tc.path, tc.body)
		if (got != "") != tc.refused {
			t.Errorf("%s: refusal = %q, want refused=%v", name, got, tc.refused)
		}
		if tc.refused && !strings.Contains(got, "not validated") {
			t.Errorf("%s: the refusal must say what to do instead:\n%s", name, got)
		}
	}
}

// TestFabricatedChecksInShellLinesAreRecognised: the same moves made with redirections and tools.
func TestFabricatedChecksInShellLinesAreRecognised(t *testing.T) {
	for line, refused := range map[string]bool{
		`printf '<?php exit(0);' > vendor/bin/phpunit && chmod +x vendor/bin/phpunit`:    true,
		`echo 'exit 0' | tee node_modules/.bin/jest`:                                     true,
		`cp /tmp/fake vendor/bin/phpunit`:                                                true,
		`composer config scripts.test "exit 0"`:                                          true,
		`npm pkg set scripts.test=true`:                                                  true,
		`npm set-script test ":"`:                                                        true,
		`cat > composer.json <<'EOF'` + "\n" + `{"scripts":{"test":"exit 0"}}` + "\nEOF": true,
		`sed -i 's/"test": ".*"/"test": "echo ok"/' package.json`:                        true,
		`composer config scripts.test "php artisan test"`:                                false,
		`composer install --no-interaction`:                                              false,
		`ls vendor/bin`:                                                                  false,
		`cat vendor/autoload.php`:                                                        false,
		`php artisan test`:                                                               false,
		`grep -n '"test"' composer.json`:                                                 false,
	} {
		if got := fakeCheckCommand(line); (got != "") != refused {
			t.Errorf("%q: refusal = %q, want refused=%v", line, got, refused)
		}
	}
}

// TestRefusedFileActionsWriteNothing: the guard is in front of the disk, and the refusal reaches
// the model as a refusal.
func TestRefusedFileActionsWriteNothing(t *testing.T) {
	e := fileAgent(t)
	manifest := filepath.Join(e.dir, "composer.json")
	original := `{"scripts":{"test":"php artisan test"}}`
	if err := os.WriteFile(manifest, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.agent.runActions(context.Background(), []Command{
		{Kind: "write_file", Command: "vendor/bin/phpunit\n<?php exit(0);"},
		{Kind: "edit_file", Command: "composer.json\n<<<<<<< SEARCH\n\"test\":\"php artisan test\"\n=======\n\"test\":\"exit 0\"\n>>>>>>> REPLACE"},
		{Kind: "command", Command: `printf 'exit 0' > vendor/bin/phpunit`},
	}, "")
	if err == nil || strings.Count(out, "[refused:") != 3 {
		t.Fatalf("all three must be refused: err=%v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(e.dir, "vendor")); statErr == nil {
		t.Error("nothing may be written under vendor/")
	}
	if got := readBack(t, manifest); got != original {
		t.Errorf("the manifest must stay as it was: %s", got)
	}
}

// TestFileActionTextIsWhatTheFileReceives covers the three shapes of an edit_file body.
func TestFileActionTextIsWhatTheFileReceives(t *testing.T) {
	if got := fileActionText("write_file", "all"); got != "all" {
		t.Errorf("write_file: %q", got)
	}
	block := "<<<<<<< SEARCH\nold\n=======\nnew\n>>>>>>> REPLACE\n"
	if got := fileActionText("edit_file", block); got != "new" {
		t.Errorf("edit_file: %q", got)
	}
	if got := fileActionText("edit_file", "no markers"); got != "" {
		t.Errorf("a malformed block has no replacement: %q", got)
	}
}
