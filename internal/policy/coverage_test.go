package policy

// The coverage of this package is a gate, not a target: every branch below is a decision the
// policy makes about somebody's machine, and an unexercised branch is a decision nobody has
// checked. These tests are written to reach the branches, and each one asserts what the
// branch DOES rather than merely calling it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellLineIsTheLastResort: it is what a line that cannot be read at all gets. Strict
// refuses it; the default lets it through as unclassified, because a line the tokeniser
// cannot read is rare and refusing everything rare makes the agent refuse real work.
func TestShellLineIsTheLastResort(t *testing.T) {
	msg := "the character \"|\" needs a shell to be interpreted"

	loose := Default().ShellLine(msg)
	if loose.Verdict != Allow {
		t.Errorf("the default must let an unreadable line through as unclassified, got %s", loose.Verdict)
	}
	if loose.Rule != "shell-line" {
		t.Errorf("rule = %q", loose.Rule)
	}
	if !strings.Contains(loose.Reason, "cannot be checked") {
		t.Errorf("the reason must say why it could not be checked: %q", loose.Reason)
	}

	strict := Mode{Enforce: true, Strict: true}.ShellLine(msg)
	if strict.Verdict != Deny {
		t.Errorf("strict must refuse an unreadable line, got %s", strict.Verdict)
	}
}

// TestAnUnreadableSegmentIsAnsweredAsAWhole: a segment the tokeniser still cannot read (an
// unclosed quote that spans a separator) is decided by the same rule, with the tokeniser's
// own reason travelling into the answer so the model can correct itself.
func TestAnUnreadableSegmentIsAnsweredAsAWhole(t *testing.T) {
	dir := t.TempDir()
	// The quote closes after the pipe, so the first segment has an unclosed quote.
	d := Default().DecideLine(`grep "sin cerrar | wc -l`, dir)
	if d.Rule != "line-unreadable" {
		t.Errorf("rule = %q, want the unreadable-segment rule: %s", d.Rule, d.Reason)
	}
	if d.Verdict != Allow {
		t.Errorf("the default answers an unreadable segment as unclassified, got %s", d.Verdict)
	}
}

// TestNestedIntroducersAreScanned: the introducers that open a command position in the middle
// of a segment, both in command position and as an argument.
func TestNestedIntroducersAreScanned(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"xargs rm -rf",
		"find . -exec rm -rf / ;",
		"find . -ok rm -rf / ;",
		"find . -execdir rm -rf / ;",
		"find . -okdir rm -rf / ;",
	} {
		if d := Default().DecideLine(line, dir); !d.Mandatory {
			t.Errorf("%q must reach the floor through its introducer, got %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}
}

// TestDestructiveVerbWithoutATarget: every floor program asked to destroy something it was
// not told about. These are the shapes that destroy the wrong thing, which is why the
// introducer is what makes them a refusal rather than a no-op.
func TestDestructiveVerbWithoutATarget(t *testing.T) {
	cases := []string{
		"xargs rm -rf",
		"xargs mkfs",
		"xargs mkfs.ext4",
		"xargs mkfs.xfs",
		"xargs dd",
		"xargs wipefs",
		"xargs blkdiscard",
		"xargs shred",
		"xargs fdisk",
		"xargs parted",
		"xargs sgdisk",
	}
	for _, line := range cases {
		d := Default().DecideLine(line, t.TempDir())
		if !d.Mandatory {
			t.Errorf("%q is a destructive verb with no target of its own and must hit the floor, got %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}
}

// TestDestructiveVerbWithATargetOfItsOwnIsNotTheIntroducerCase: `xargs rm -rf build` names
// its target, so it is ordinary work and must not be refused.
func TestDestructiveVerbWithATargetOfItsOwnIsNotTheIntroducerCase(t *testing.T) {
	dir := t.TempDir()
	d := Default().DecideLine("xargs rm -rf build", dir)
	if d.Mandatory {
		t.Errorf("a target of its own makes it local work: %s", d.Reason)
	}
}

func TestHasFlagReadsClusters(t *testing.T) {
	if !hasFlag([]string{"-rf"}, 'r') || !hasFlag([]string{"-rf"}, 'f') {
		t.Error("a cluster carries both flags")
	}
	if hasFlag([]string{"-r"}, 'f') {
		t.Error("-r does not carry f")
	}
	if hasFlag([]string{"--force"}, 'f') {
		t.Error("a long option is not a short cluster")
	}
	if hasFlag([]string{"archivo"}, 'f') {
		t.Error("a bare word is not a flag")
	}
}

// TestRootLikeTargetForTheOtherVerbs: `mv`, `cp`, `install` and `ln` reach the same place as
// `rm` from a different direction, and the floor has to see it whichever verb is used.
func TestRootLikeTargetForTheOtherVerbs(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"mv / /tmp/x",
		"cp -r . /",
		"ln -s / enlace",
		"install -m 777 x /etc",
	} {
		d := Default().DecideLine(line, dir)
		if !d.Mandatory {
			t.Errorf("%q targets a tree the agent must not touch, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

func TestRootLikeTargetLeavesLocalWorkAlone(t *testing.T) {
	dir := t.TempDir()
	if _, bad := rootLikeTarget([]string{filepath.Join(dir, "a"), "b.txt"}, dir); bad {
		t.Error("targets inside the workspace are local work")
	}
}

// TestFirstNonFlagFindsTheSubcommand: the argument that decides what a tool will do.
func TestFirstNonFlagFindsTheSubcommand(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"push", "origin"}, "push"},
		{[]string{"-v", "status"}, "status"},
		{[]string{"--flag", "--other", "run"}, "run"},
		{[]string{"-v"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstNonFlag(tc.args); got != tc.want {
			t.Errorf("firstNonFlag(%#v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// TestUnquoteStripsOnePair: a path written inside quotes carries them into the comparison,
// and a path with a stray quote at one end must not be mangled.
func TestUnquoteStripsOnePair(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"a b.txt"`, "a b.txt"},
		{`'a b.txt'`, "a b.txt"},
		{`"a`, `"a`}, // one quote is not a pair
		{`a"`, `a"`}, // and neither is a closing one alone
		{`""`, ""},   // an empty quoted string is an empty path
		{`a`, "a"},
	}
	for _, tc := range cases {
		if got := unquote(tc.in); got != tc.want {
			t.Errorf("unquote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAQuotedRedirectTargetIsJudgedByItsPath: the quotes are syntax, not part of the path.
func TestAQuotedRedirectTargetIsJudgedByItsPath(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "con espacio.txt")
	d := Default().DecideLine(`echo x > "`+outside+`"`, dir)
	if d.Verdict != Ask {
		t.Errorf("a quoted path outside the workspace must still be asked about, got %s: %s",
			d.Verdict, d.Reason)
	}
}

// TestCommandArgsStopsAtTheNextCommand: handing the first command the arguments of the second
// is how `rm -rf a; rm -rf /` would look like a targeted delete.
func TestCommandArgsStopsAtTheNextCommand(t *testing.T) {
	cases := []struct {
		rest []string
		want int
	}{
		{[]string{"-rf", "build"}, 2},
		{[]string{"-rf", "a", ";", "rm"}, 2},
		{[]string{"-rf", "a", "&&", "true"}, 2},
		{[]string{"-rf", "a", "xargs", "rm"}, 2},
		{[]string{"-rf", "a", "sudo", "rm"}, 2},
	}
	for _, tc := range cases {
		if got := commandArgs(tc.rest); len(got) != tc.want {
			t.Errorf("commandArgs(%#v) = %#v, want %d items", tc.rest, got, tc.want)
		}
	}
}

// TestTheFloorDecidesEveryVerbItNames: the verb table is what makes a program a floor
// program once the scan has found it, so each verb has to actually refuse.
func TestTheFloorDecidesEveryVerbItNames(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"sfdisk /dev/sda",
		"cfdisk /dev/sda",
		"gdisk /dev/sda",
		"mkfs.btrfs /dev/sda",
		"mkfs.vfat /dev/sda",
		"dd of=/dev/sda",
	} {
		if d := Default().DecideLine(line, dir); !d.Mandatory {
			t.Errorf("%q is on the floor and must be refused outright, got %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}
}

// TestWriteTargetsForTheVerbsWithoutAPath: a writer whose arguments name no destination
// cannot be called local work, so it falls to the unclassified rule instead of being guessed
// at. Each of these names the verbs that HAVE a target, to prove the extraction works.
func TestWriteTargetsForTheVerbsWithoutAPath(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "x")
	for _, line := range []string{
		"rmdir " + inside,
		"mkdir " + filepath.Join(dir, "d"),
		"touch " + inside,
		"truncate -s 0 " + inside,
		"chmod 644 " + inside,
		"chown user " + inside,
		"chgrp group " + inside,
		"ln -s a " + inside,
		"mkfifo " + filepath.Join(dir, "f"),
		"mknod " + filepath.Join(dir, "n"),
		"tee " + inside,
		"install -m 755 a " + inside,
	} {
		d := Default().DecideLine(line, dir)
		if d.Rule != "write-inside-workspace" {
			t.Errorf("%q writes inside the workspace and must be allowed by that rule, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestIsRootWriteTargetCoversTheSystemTrees: the write side of the floor, which is about a
// file rather than a tree — `/etc/passwd` is not a tree, and overwriting it is still not a
// thing the agent may do.
func TestIsRootWriteTargetCoversTheSystemTrees(t *testing.T) {
	cases := map[string]bool{
		"/etc/passwd":  true,
		"/etc":         true,
		"/usr/bin/sh":  true,
		"/bin/sh":      true,
		"/sbin/init":   true,
		"/boot/grub":   true,
		"/lib/x.so":    true,
		"/lib64/x.so":  true,
		"/var/log/x":   true,
		"/root/.ssh/k": true,
		"/opt/app/x":   true,
		"/srv/www/x":   true,
		"/":            true,
		"~":            true,
		"~/x":          true,
		"/dev/null":    false, // a harmless device is not a tree
		"":             false,
	}
	for in, want := range cases {
		if got := isRootWriteTarget(in); got != want {
			t.Errorf("isRootWriteTarget(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestResolveAsFarAsPossibleFollowsWhatExists: the deepest existing ancestor is followed
// through its links. It is the rule that stops a symlink inside the workspace from being a
// way out of it, and it has to work on a path that is part root and part not-yet-created.
func TestResolveAsFarAsPossibleFollowsWhatExists(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "enlace")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	// The file does not exist yet; the link does.
	got := resolveAsFarAsPossible(filepath.Join(link, "todavia", "no", "existe.txt"))
	want := filepath.Join(real, "todavia", "no", "existe.txt")
	if got != want {
		t.Errorf("resolveAsFarAsPossible = %q, want %q", got, want)
	}

	// A path that exists resolves completely, and one that exists nowhere at all is returned
	// as written (cleaned) rather than mangled.
	abs := filepath.Join(real, "existe.txt")
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveAsFarAsPossible(abs); got != abs {
		t.Errorf("an existing path resolves to itself here, got %q", got)
	}
}

func TestResolveAsFarAsPossibleOnARootThatDoesNotExist(t *testing.T) {
	// Nothing under this path exists, all the way to the root: the walk has to terminate and
	// return the path rather than loop.
	got := resolveAsFarAsPossible("/nada-de-esto-existe-99/a/b/c")
	if got != "/nada-de-esto-existe-99/a/b/c" {
		t.Errorf("got %q", got)
	}
}

// TestHarmlessDevices: the devices whose write destroys nothing. Refusing `> /dev/null` would
// be the false alarm that teaches a user to switch the policy off.
func TestHarmlessDevices(t *testing.T) {
	for _, p := range []string{
		"/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/full",
		"/dev/fd/3", "/dev/pts/0",
	} {
		if !isHarmlessDevice(p) {
			t.Errorf("%q destroys nothing and must be treated as harmless", p)
		}
	}
	for _, p := range []string{"/dev/sda", "/dev/nvme0n1", "/dev/mmcblk0", "/tmp/x", ""} {
		if isHarmlessDevice(p) {
			t.Errorf("%q is not a harmless device", p)
		}
	}
}

// TestDecisionForOnAnEmptyCommand is the entry point's own empty case, which the line-level
// check also covers but which a direct caller must get the same answer from.
func TestDecisionForOnAnEmptyCommand(t *testing.T) {
	d := Default().DecisionFor("", nil, t.TempDir())
	if d.Verdict != Deny || d.Rule != "missing" {
		t.Errorf("an empty command must be refused as missing, got %s (rule %s)", d.Verdict, d.Rule)
	}
	// A bare directory name is not a program either.
	d = Default().DecisionFor("/", nil, t.TempDir())
	if d.Verdict != Deny {
		t.Errorf("a lone slash is not a command, got %s", d.Verdict)
	}
}

// TestDecideLineWithARedirectOnly: a line that is nothing but a redirection has no command to
// run, and the redirect itself still has to be judged — `> /etc/passwd` must not slip through
// because there was nothing before it.
func TestDecideLineWithARedirectOnly(t *testing.T) {
	dir := t.TempDir()
	d := Default().DecideLine("> /etc/passwd", dir)
	if d.Verdict != Deny || !d.Mandatory {
		t.Errorf("a bare write to a system file must be refused outright, got %s (rule %s)", d.Verdict, d.Rule)
	}
	inside := filepath.Join(dir, "salida.txt")
	if d := Default().DecideLine("> "+inside, dir); d.Verdict != Allow {
		t.Errorf("a bare write inside the workspace is work, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestDecideLineStopsAtTheWorstVerdict: once a segment has earned a mandatory refusal the
// rest of the line is not analysed, and the answer keeps that rule rather than a later one.
func TestDecideLineKeepsTheDecidingRule(t *testing.T) {
	dir := t.TempDir()
	d := Default().DecideLine("rm -rf / && ls", dir)
	if d.Rule != "mandatory" || !d.Mandatory {
		t.Errorf("the floor must be the deciding rule, got %s (rule %s)", d.Verdict, d.Rule)
	}
}

// TestSegmentationOfQuotesAndEscapes reaches the scanner's own branches: an escape, a quote
// that ends, and a quote that never ends.
func TestSegmentationOfQuotesAndEscapes(t *testing.T) {
	cases := []struct {
		line     string
		commands int
		redirect int
	}{
		{`echo "a \" dentro"`, 1, 0},
		{`echo 'a b'`, 1, 0},
		{`echo "sin cerrar`, 1, 0},
		{`echo a\ b`, 1, 0},
		{"echo 'a\"b'", 1, 0},
		{"> f", 0, 1},
		{"a >", 1, 1},
		{"1> f", 0, 1}, // a descriptor with no command before it is only a redirection
		{"2>> f", 0, 1},
		{"a >&2", 1, 1},
	}
	for _, tc := range cases {
		segs := lineSegments(tc.line)
		commands, redirects := 0, 0
		for _, s := range segs {
			if s.redirect {
				redirects++
			} else {
				commands++
			}
		}
		if commands != tc.commands || redirects != tc.redirect {
			t.Errorf("lineSegments(%q) = %d commands, %d redirects, want %d and %d (%#v)",
				tc.line, commands, redirects, tc.commands, tc.redirect, segs)
		}
	}
}

// TestARedirectWithNoTarget: `a >` alone has an operator and no destination. The segment
// exists so that the line is not read as a command with a stray argument, and the redirect
// rule answers it without a file to judge.
func TestARedirectWithNoTarget(t *testing.T) {
	segs := lineSegments("a >")
	redirects := 0
	for _, s := range segs {
		if s.redirect {
			redirects++
			if len(s.words) != 1 || s.words[0] != "" {
				t.Errorf("the target is empty, got %#v", s.words)
			}
		}
	}
	if redirects != 1 {
		t.Fatalf("one redirect expected, got %#v", segs)
	}

	dir := t.TempDir()
	if d := Default().DecideLine("ls >", dir); d.Verdict == Deny && d.Mandatory {
		t.Errorf("a redirect with no destination must not be a floor refusal: %s", d.Reason)
	}

	// The same operator immediately followed by the next command (`>;`), with no space to
	// close it. The redirection is still closed with an empty target, and the command after
	// the separator is still read as a command.
	segs = lineSegments("echo x >; ls")
	var commands, empties int
	for _, s := range segs {
		if s.redirect {
			empties++
			if len(s.words) != 1 || s.words[0] != "" {
				t.Errorf("the target is empty, got %#v", s.words)
			}
			continue
		}
		commands++
	}
	if commands != 2 || empties != 1 {
		t.Errorf("lineSegments(\"echo x >; ls\") = %d commands, %d redirects, want 2 and 1 (%#v)",
			commands, empties, segs)
	}
}

// TestWritingFormWithNoTargetsIsUnclassified: the branch where the arguments do not locate
// the change at all. It must not be allowed by default, and it must not be refused either —
// it is exactly what the unclassified verdict is for.
func TestWritingFormWithNoTargetsIsUnclassified(t *testing.T) {
	if _, hit := writingForm("chmod", []string{"644"}, t.TempDir()); hit {
		t.Error("a target-less writer must fall through to the unclassified rule")
	}
	if _, hit := writingForm("totalmente-otro", nil, t.TempDir()); hit {
		t.Error("only the listed writers have their targets extracted")
	}
	if _, hit := writingForm("mv", nil, t.TempDir()); hit {
		t.Error("mv with no operands has no destination to judge")
	}
}

// TestAWriterWithNoLocatableTargetIsAskedAboutInTheCautiousMode: strict refuses it, the
// default lets it through as unclassified, and neither runs it silently by accident.
func TestAWriterWithNoLocatableTargetInBothModes(t *testing.T) {
	dir := t.TempDir()
	loose := Mode{Enforce: true}.DecideLine("chmod 644", dir)
	if loose.Verdict != Allow || loose.Rule != "writer-unclassified" {
		t.Errorf("the default answers an unplaceable writer as unclassified, got %s (rule %s)",
			loose.Verdict, loose.Rule)
	}
	strict := Mode{Enforce: true, Strict: true}.DecideLine("chmod 644", dir)
	if strict.Verdict != Deny {
		t.Errorf("strict must refuse it, got %s", strict.Verdict)
	}
}

// TestExternalToolsWithoutASubcommand: `git` with no arguments has no subcommand to consult,
// and an unlisted subcommand is external. Both are the same answer: external.
func TestExternalToolsWithoutASubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"--version"}, {"filter-branch"}, {"submodule", "add"}} {
		if !reachesOutside("git", args) {
			t.Errorf("git %#v has no local verb and must count as external", args)
		}
	}
	// A program absent from the subcommand map is external whatever its arguments say.
	if !reachesOutside("curl", []string{"http://x"}) {
		t.Error("curl is external by nature")
	}
	if reachesOutside("ls", nil) {
		t.Error("ls is not external")
	}
}

// TestTheSubcommandTablesDecideBothWays: each tool that has a local life and a remote one,
// checked in both directions. One direction alone would pass with a table that always
// answered the same thing.
func TestTheSubcommandTablesDecideBothWays(t *testing.T) {
	cases := []struct {
		name   string
		local  []string
		remote []string
	}{
		{"hg", []string{"status", "log", "diff"}, []string{"push", "commit"}},
		{"svn", []string{"status", "log", "diff"}, []string{"commit", "update"}},
		{"go", []string{"test", "build", "vet", "fmt", "list", "doc", "version", "env", "run", "generate"}, []string{"get", "install"}},
		{"npm", []string{"test", "run", "run-script", "exec", "ls", "list", "view", "outdated", "audit"}, []string{"publish", "install"}},
		{"cargo", []string{"test", "build", "check", "clippy", "fmt", "run", "doc", "tree"}, []string{"publish", "install"}},
		{"pip", []string{"list", "show", "freeze"}, []string{"install"}},
		{"pip3", []string{"list", "show", "freeze"}, []string{"install"}},
		{"yarn", []string{"test", "run", "list", "why", "outdated"}, []string{"publish", "add"}},
		{"docker", []string{"ps", "images", "inspect", "logs", "version", "info"}, []string{"run", "rm"}},
		{"kubectl", []string{"get", "describe", "logs", "version", "explain"}, []string{"apply", "delete"}},
		{"kill", nil, []string{"1234"}},
		{"mount", nil, []string{"/dev/sda"}},
	}
	for _, tc := range cases {
		for _, sub := range tc.local {
			if reachesOutside(tc.name, []string{sub}) {
				t.Errorf("%s %s is local work and must not be asked about", tc.name, sub)
			}
		}
		for _, sub := range tc.remote {
			if !reachesOutside(tc.name, []string{sub}) {
				t.Errorf("%s %s reaches outside and must be asked about", tc.name, sub)
			}
		}
	}
}

// TestGitLocalVerbs: the table that matters most in this repository, checked in full.
func TestGitLocalVerbs(t *testing.T) {
	local := []string{
		"status", "log", "diff", "show", "branch", "remote", "tag", "describe",
		"rev-parse", "blame", "shortlog", "ls-files", "cat-file", "config", "grep",
		"whatchanged", "reflog", "show-ref", "for-each-ref", "count-objects",
		"add", "commit", "checkout", "switch", "restore", "stash", "merge", "rebase",
		"init", "clone", "worktree", "rm", "mv",
	}
	for _, sub := range local {
		if reachesOutside("git", []string{sub}) {
			t.Errorf("git %s is local work", sub)
		}
	}
	for _, sub := range []string{"push", "reset", "clean", "filter-branch", "gc", "submodule"} {
		if !reachesOutside("git", []string{sub}) {
			t.Errorf("git %s reaches outside what the user can see from here", sub)
		}
	}
}

// TestTheExternalTableOnlyHoldsRealPrograms keeps the list honest: every name in it must be a
// program the classifier actually places, because a typo there would mean the external
// question is never asked for the program the typo was meant to name.
func TestTheExternalTableOnlyHoldsRealPrograms(t *testing.T) {
	for name := range externalPrograms {
		if name != strings.ToLower(strings.TrimSpace(name)) || name == "" {
			t.Errorf("external program %q is not a usable name", name)
		}
		if name != baseName(name) {
			t.Errorf("external program %q must be listed by its bare name", name)
		}
	}

	// A program that only reads must never be listed as external: asking about a log read is
	// the false alarm this design exists to avoid.
	if externalPrograms["journalctl"] {
		t.Error("journalctl only reads the log and must not be external")
	}

	// The writers whose effect has no path in its arguments are the reason the rule exists,
	// and they have to be listed, or their effect would be judged as a local file write.
	for _, name := range []string{"curl", "wget", "ssh", "scp", "rsync", "systemctl", "crontab"} {
		if !externalPrograms[name] {
			t.Errorf("%q changes something outside the workspace with no path in its arguments "+
				"and must be listed as external", name)
		}
	}
}

// TestFloorProgramsAreAllReal: the scan searches for these names, so a typo would be a hole.
func TestFloorProgramsAreAllReal(t *testing.T) {
	dir := t.TempDir()
	for name := range floorPrograms {
		if strings.TrimSpace(name) == "" || name != strings.ToLower(name) {
			t.Errorf("floor program %q is not a usable name", name)
		}
		// Every one of them must be reachable through the line scan, which is the entry
		// point the agent uses, in its dangerous form.
		line := name + " " + strings.Join(destructiveArgs(name), " ")
		if d := Default().DecideLine(line, dir); !d.Mandatory {
			t.Errorf("%q is in floorPrograms but the scan does not refuse %q (rule %s)",
				name, line, d.Rule)
		}
	}
}

// TestACommandWithAnArgumentContainingEquals: `FOO=bar` is an assignment, and the scan must
// not read it as a program name. A floor program written as an assignment is still found.
func TestACommandWithAnArgumentContainingEquals(t *testing.T) {
	dir := t.TempDir()
	if d := Default().DecideLine("FOO=1 ls", dir); d.Verdict != Allow {
		t.Errorf("an assignment followed by a reader is work, got %s: %s", d.Verdict, d.Reason)
	}
	d := Default().DecideLine("FOO=1 rm -rf /", dir)
	if !d.Mandatory {
		t.Errorf("an assignment must not hide the floor, got %s (rule %s)", d.Verdict, d.Rule)
	}
}

// TestANumericWrapperOperandIsNotTheProgram: `nice -n 10 rm` must still find rm.
func TestANumericWrapperOperandIsNotTheProgram(t *testing.T) {
	dir := t.TempDir()
	if d := Default().DecideLine("nice -n 10 rm -rf /", dir); !d.Mandatory {
		t.Errorf("the wrapper's operand must not become the program, got %s (rule %s)", d.Verdict, d.Rule)
	}
}

// TestANestedShellPayloadIsScannedFromItsOwnStart: `sh -c 'sh -c "rm -rf /"'` nests, and the
// scan follows it in.
func TestANestedShellPayloadIsScannedFromItsOwnStart(t *testing.T) {
	dir := t.TempDir()
	d := Default().DecideLine(`sh -c 'sh -c "rm -rf /"'`, dir)
	if !d.Mandatory {
		t.Errorf("a nested shell payload must still reach the floor, got %s (rule %s)", d.Verdict, d.Rule)
	}
}

// TestTheScanTerminatesOnPathologicalInput: the scanner runs on model-written text, so it
// must not be possible to make it loop or blow up.
func TestTheScanTerminatesOnPathologicalInput(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		strings.Repeat(">", 1000),
		strings.Repeat("'", 1000),
		strings.Repeat("\\", 1000),
		strings.Repeat("|", 1000),
		strings.Repeat(" ", 1000),
		strings.Repeat("a", 10000),
		"rm -rf " + strings.Repeat("../", 1000),
	} {
		_ = Default().DecideLine(line, dir)
	}
}

// TestWorseKeepsTheFirstOnATie: two Allows are the same answer, and the reason that travels
// with the line must be one a segment actually gave.
func TestWorseKeepsTheFirstOnATie(t *testing.T) {
	a := Decision{Allow, "primero", "a", false}
	b := Decision{Allow, "segundo", "b", false}
	if got := worse(a, b); got.Reason != "primero" {
		t.Errorf("a tie keeps the first verdict, got %q", got.Reason)
	}
	if got := worse(a, Decision{Ask, "pregunta", "c", false}); got.Verdict != Ask {
		t.Errorf("Ask beats Allow, got %s", got.Verdict)
	}
	if got := worse(Decision{Ask, "p", "c", false}, Decision{Deny, "no", "d", false}); got.Verdict != Deny {
		t.Errorf("Deny beats Ask, got %s", got.Verdict)
	}
	// A mandatory refusal travelling as the second argument still wins.
	mand := Decision{Deny, "piso", "mandatory", true}
	if got := worse(a, mand); !got.Mandatory {
		t.Error("a mandatory refusal must win over an Allow")
	}
}

// TestAnUnknownProgramThatReachesNowhereIsUnclassified: the default branch of DecisionFor,
// which is what an unrecognised program gets. Asking about every program the list does not
// know would make the policy unusable, so the answer is the unclassified one — allowed by
// default, refused in strict mode.
func TestAnUnknownProgramThatReachesNowhereIsUnclassified(t *testing.T) {
	dir := t.TempDir()
	d := Default().Decide(commandForTest, nil, dir)
	if d.Verdict != Allow || d.Rule != "unclassified" {
		t.Errorf("an unknown local program answers unclassified, got %s (rule %s)", d.Verdict, d.Rule)
	}
	strict := Mode{Enforce: true, Strict: true}.Decide(commandForTest, nil, dir)
	if strict.Verdict != Deny {
		t.Errorf("strict must refuse it, got %s", strict.Verdict)
	}
}

// commandForTest is a program name nothing in the policy knows, which is what makes it reach
// the unclassified branch.
const commandForTest = "programa-que-nadie-clasifico"

// TestAWrapperOperandIsNotTheProgram: `nice -n 10 rm -rf /` must find rm behind the wrapper's
// own numeric operand, and `env FOO=1 rm -rf /` behind an assignment.
func TestAWrapperOperandIsNotTheProgram(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"nice -n 10 rm -rf /",
		"env FOO=1 rm -rf /",
		"stdbuf -o0 rm -rf /",
		"setsid rm -rf /",
		"command rm -rf /",
	} {
		if d := Default().DecideLine(line, dir); !d.Mandatory {
			t.Errorf("%q hides the floor behind a wrapper operand, got %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}
}

// TestLineArgsStopsAtASeparator: the operands of the line belong to the program only until
// the next command begins.
func TestLineArgsStopsAtASeparator(t *testing.T) {
	tokens := []string{"rm", "-rf", "build", ";", "rm", "-rf", "dist"}
	got := lineArgs(tokens, 1)
	if len(got) != 2 || got[1] != "build" {
		t.Errorf("lineArgs = %#v, want the operands up to the separator", got)
	}
	if got := lineArgs([]string{"rm"}, 1); len(got) != 0 {
		t.Errorf("nothing follows the program, got %#v", got)
	}
}

// TestADestructiveVerbInsideAnIntroducerWithoutItsOwnOperands: the shapes that have no target
// to read, checked one by one so a missing case in the table is a failure rather than a
// silent hole.
func TestADestructiveVerbInsideAnIntroducerWithoutItsOwnOperands(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"xargs rm -rf", "xargs rm -r -f", "xargs -n 1 rm -rf", "xargs --max-args 1 rm -rf",
		"xargs dd", "xargs shred", "xargs wipefs", "xargs blkdiscard",
		"xargs mkfs", "xargs mkfs.ext2", "xargs mkfs.ext3", "xargs mkfs.ext4",
		"xargs mkfs.xfs", "xargs mkfs.btrfs", "xargs mkfs.vfat",
		"xargs fdisk", "xargs sfdisk", "xargs parted", "xargs gdisk", "xargs sgdisk",
	} {
		if d := Default().DecideLine(line, dir); !d.Mandatory {
			t.Errorf("%q names no target and is fed by an introducer, so the floor must refuse it "+
				"(got %s, rule %s)", line, d.Verdict, d.Rule)
		}
	}
}

// TestARootLikeTargetIsResolvedThroughLinks: a path that reads as local but points at a tree
// the agent must not touch, through a symlink or through `..`.
func TestARootLikeTargetIsResolvedThroughLinks(t *testing.T) {
	dir := t.TempDir()
	for _, target := range []string{"/", "/home", "/etc", "/usr", "/var", "/root", "~", "~/x"} {
		if !isRootLike(target, dir) {
			t.Errorf("%q is not local work", target)
		}
	}
	// The parent of the workspace, which is what `..` reaches.
	parent := filepath.Dir(dir)
	if !isRootLike(parent, dir) {
		t.Errorf("the tree above the workspace (%q) must not be treated as local work", parent)
	}
	// A path inside the workspace is work.
	if isRootLike(filepath.Join(dir, "x"), dir) {
		t.Error("a path inside the workspace is local work")
	}
	// An empty target names nothing.
	if isRootLike("", dir) {
		t.Error("an empty target is not a tree")
	}
}

// TestFirstOutsideWithoutAWorkspace: a caller that does not know where the workspace is gets
// no opinion from the workspace rule, rather than a refusal of everything.
func TestFirstOutsideWithoutAWorkspace(t *testing.T) {
	if got := firstOutside([]string{"/etc/passwd"}, ""); got != "" {
		t.Errorf("with no workspace there is nothing to be outside of, got %q", got)
	}
	if got := firstOutside(nil, t.TempDir()); got != "" {
		t.Errorf("no targets, nothing outside, got %q", got)
	}
	// An empty target is skipped rather than compared.
	if got := firstOutside([]string{""}, t.TempDir()); got != "" {
		t.Errorf("an empty target is not outside anything, got %q", got)
	}
}

// TestResolveAsFarAsPossibleOnARootThatCannotBeResolved covers the case where nothing on the
// path exists at all: the walk reaches the root and returns the path as written.
func TestResolveAsFarAsPossibleOnARootThatCannotBeResolved(t *testing.T) {
	got := resolveAsFarAsPossible("/no-existe-99/a/b")
	if got != "/no-existe-99/a/b" {
		t.Errorf("got %q", got)
	}
}

// TestAnUnknownProgramThatDoesReachOutside: the default branch of DecisionFor when the
// program is in the external list. `telnet`, `socat` and `vagrant` are the shape this covers —
// nobody classified them as readers or writers, and their effect is still not a file.
func TestAnUnknownProgramThatDoesReachOutside(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"telnet", "socat", "vagrant"} {
		d := Default().DecideLine(name+" x", dir)
		if d.Verdict != Ask || d.Rule != "external-effect" {
			t.Errorf("%q has no local life and must be asked about, got %s (rule %s)",
				name, d.Verdict, d.Rule)
		}
	}
}

// TestARedirectionFollowedByTheNextCommand: a redirection's target is taken before the
// separator that starts the next command, so `echo x > f | wc -l` is one command writing to f
// and one reader, not a command called `f`.
func TestARedirectionFollowedByTheNextCommand(t *testing.T) {
	dir := t.TempDir()
	// The target exists, so the line writes inside the workspace and reads after it.
	d := Default().DecideLine("echo x > f | wc -l", dir)
	if d.Verdict != Allow {
		t.Errorf("a write inside the workspace piped into a reader is work, got %s: %s", d.Verdict, d.Reason)
	}
	// And the redirect target is not mistaken for a command: a line whose target is a
	// program name must not be judged as that program.
	outside := filepath.Join(filepath.Dir(dir), "remoto")
	d = Default().DecideLine("echo x > "+outside+" ; wc -l", dir)
	if d.Verdict != Ask || d.Rule != "write-outside-workspace" {
		t.Errorf("the write outside is what decides, got %s (rule %s): %s", d.Verdict, d.Rule, d.Reason)
	}
}

// TestDestructiveVerbOnAProgramWithNoSuchForm: the fall-through, which is what any floor
// program that is refused whatever its operands say gets. `mkfs` and `shred` never reach this
// question because mandatory() refuses them outright.
func TestDestructiveVerbOnAProgramWithNoSuchForm(t *testing.T) {
	for _, name := range []string{"cp", "mkfs", "shred", "wipefs", "fdisk", "sgdisk", "ln", "mv"} {
		if destructiveVerb(name, nil) {
			t.Errorf("%q is refused whatever its operands say, so it must not reach this question", name)
		}
	}
	// `rm` without both flags is not destructive, and `rm` with a target of its own is work.
	if destructiveVerb("rm", []string{"-r", "build"}) {
		t.Error("without --force it is not the target-less delete")
	}
	if destructiveVerb("rm", []string{"-rf", "build"}) {
		t.Error("a target of its own makes it a local delete")
	}
	if destructiveVerb("dd", []string{"of=/dev/sda"}) {
		t.Error("dd with an output target is decided by the floor, not by this question")
	}
}

// TestAHomeDirectoryOfAUserIsRootLike: `/home/alguien` is a home, not local work, and it is
// neither the bare `/home` nor a path inside one.
func TestAHomeDirectoryOfAUserIsRootLike(t *testing.T) {
	dir := t.TempDir()
	for _, target := range []string{"/home/alguien", "/home/madkoding"} {
		if !isRootLike(target, dir) {
			t.Errorf("%q is somebody's home and not local work", target)
		}
	}
	// A path INSIDE a home is not the home itself.
	if isRootLike("/home/alguien/x", dir) {
		t.Error("a file inside a home is not the home itself")
	}
}

// TestFirstOutsideWithARelativeWorkspace: a workspace given as a relative path still compares
// against absolute targets, instead of treating every one of them as outside.
func TestFirstOutsideWithARelativeWorkspace(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, wd)
	if err != nil {
		t.Fatal(err)
	}
	// The workspace IS the working directory, written relatively, and a target inside it is
	// not outside.
	if got := firstOutside([]string{filepath.Join(wd, "dentro.txt")}, rel); got != "" {
		t.Errorf("a target inside the workspace must not be outside, got %q", got)
	}
	// And a target outside it still is.
	if got := firstOutside([]string{filepath.Dir(wd)}, rel); got == "" {
		t.Error("the parent of the workspace is outside it")
	}
}
