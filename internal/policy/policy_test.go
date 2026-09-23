package policy

// The tests are written around the EFFECT question rather than the decision: what a command
// would do to the machine. A policy that returns the right verdict and lets the action run
// anyway has failed, and a table of verdicts cannot tell the difference.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testMode is the mode the tests judge commands with.
//
// It is built HERE rather than taken from production on purpose: these tests are about what the
// POLICY decides, so the mode they hand it must not be a thing a production change can move under
// them. Enforce on and Strict off is the behaviour under test — the tests that need the other
// combinations build them explicitly, which is why none of them ever asked for a "default".
func testMode() Mode { return Mode{Enforce: true, Strict: false} }

func TestVerdictString(t *testing.T) {
	cases := map[Verdict]string{Allow: "allow", Ask: "ask", Deny: "deny"}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
	// The zero value is Allow, which is the load-bearing property of the ordering: a
	// Decision nobody filled in must not read as a refusal.
	var zero Decision
	if zero.Verdict != Allow {
		t.Errorf("the zero verdict must be Allow, got %s", zero.Verdict)
	}
}

// --- the mandatory floor ----------------------------------------------------

// TestTheMandatoryFloorSurvivesEverySetting is the property the floor exists for. It is
// checked against EVERY combination of the two settings, because the failure mode is a
// particular combination someone thought was safe.
func TestTheMandatoryFloorSurvivesEverySetting(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		"mkfs.ext4 /dev/sda1",
		"mkfs /dev/sda",
		"fdisk /dev/sda",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		"reboot",
		"poweroff",
		"halt",
		"init 0",
		"wipefs -a /dev/sda",
		"blkdiscard /dev/sda",
		"shred -u /etc/passwd",
		"rm -rf /",
		"rm -rf /*",
		"rm -rf ..",
		"rm -rf ../",
		"rm -rf /home",
		"rm -rf /etc",
		"rm -rf ~",
		"rm -fr /",
		"rm -r -f /",
		"rm --recursive --force /",
	}
	for _, enforce := range []bool{true, false} {
		for _, strict := range []bool{true, false} {
			m := Mode{Enforce: enforce, Strict: strict}
			for _, line := range lines {
				d := m.DecideLine(line, dir)
				if d.Verdict != Deny {
					t.Errorf("enforce=%v strict=%v: %q must be Denied, got %s (rule %s)",
						enforce, strict, line, d.Verdict, d.Rule)
					continue
				}
				if !d.Mandatory {
					t.Errorf("enforce=%v strict=%v: the refusal of %q must be Mandatory",
						enforce, strict, line)
				}
			}
		}
	}
}

// TestTheMandatoryFloorIsReachableThroughAShell: the per-program check can never see these,
// because the tokeniser refuses the line before any program is named. A floor that only
// fired on a well-formed command would be walked past by the ordinary way of reaching one.
func TestTheMandatoryFloorIsReachableThroughAShell(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		`sh -c 'rm -rf /'`,
		`bash -c "rm -rf /"`,
		`sh -c 'mkfs.ext4 /dev/sda'`,
		`sudo sh -c 'reboot'`,
		`ls; rm -rf /`,
		`echo hi && rm -rf /`,
		`true | rm -rf /`,
		`xargs rm -rf`,
		`find . -exec rm -rf / {} ;`,
		`sh -c 'sh -c "rm -rf /"'`,
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Deny || !d.Mandatory {
			t.Errorf("%q must hit the mandatory floor, got %s (rule %s, mandatory %v)",
				line, d.Verdict, d.Rule, d.Mandatory)
		}
	}
}

// TestMentioningAFloorCommandIsNotRunningIt: the scan ignores argument position, so a line
// that merely talks about one is not refused. Without this the floor would be a rule about
// words, and the agent could not write about a backup script without being stopped.
func TestMentioningAFloorCommandIsNotRunningIt(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		`echo "never run rm -rf / on a server"`,
		`grep -rn "mkfs" README.md`,
		`cat notes.txt`,
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict == Deny && d.Mandatory {
			t.Errorf("%q only mentions a floor command and must not be refused: %s", line, d.Reason)
		}
	}
}

// TestWritingToADeviceIsRefused: `> /dev/sda` destroys a disk as thoroughly as dd does, and
// it is one keystroke. It is a mandatory refusal, not a question.
func TestWritingToADeviceIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"echo x > /dev/sda",
		"cat image.iso > /dev/sdb",
		"echo x >> /dev/nvme0n1",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Deny || !d.Mandatory {
			t.Errorf("%q must be refused outright, got %s (rule %s)", line, d.Verdict, d.Rule)
		}
	}
}

// TestWritingToASystemTreeIsRefused: what the workspace rule cannot allow, because there is
// no version of it that is local work.
func TestWritingToASystemTreeIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"echo x > /etc/passwd",
		"echo x > /usr/bin/sh",
		"echo x > /boot/grub.cfg",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Deny || !d.Mandatory {
			t.Errorf("%q must be refused outright, got %s (rule %s)", line, d.Verdict, d.Rule)
		}
	}
}

// TestTheFloorLeavesOrdinaryWorkAlone: `rm -rf` of a directory inside the workspace is
// exactly what a cleanup task IS asked to do. A floor that refused it would refuse the work.
func TestTheFloorLeavesOrdinaryWorkAlone(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"rm -rf ./build",
		"rm -rf build",
		"rm -rf " + filepath.Join(dir, "vendor"),
		"rm -f file.txt",
		"rm file.txt",
		"rmdir empty",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Mandatory {
			t.Errorf("%q is local work and must not hit the mandatory floor: %s", line, d.Reason)
		}
	}
}

// TestRmWithoutBothFlagsIsNotTheFloor: `rm -r` alone prompts, `rm -f` alone does not recurse.
// The floor is about the combination that removes a tree without a word.
func TestRmWithoutBothFlagsIsNotTheFloor(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"rm -r /home", "rm -f /etc/passwd"} {
		if d := testMode().DecideLine(line, dir); d.Mandatory {
			t.Errorf("%q lacks recursive-and-forced and must not hit the floor: %s", line, d.Reason)
		}
	}
}

// TestDdThatOnlyReadsIsUntouched: `dd if=image of=out` inside the workspace is how a disk
// image is written, which is work. Only an output target on its own is the floor.
func TestDdThatOnlyReadsIsUntouched(t *testing.T) {
	dir := t.TempDir()
	if d := testMode().DecideLine("dd if=/dev/zero count=1", dir); d.Mandatory {
		t.Errorf("a dd with no target writes nothing and must not hit the floor: %s", d.Reason)
	}
}

// --- readers, writers and where they land -----------------------------------

func TestReadersRunWithoutBeingAsked(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"ls -la",
		"cat README.md",
		"grep -rn TODO internal/",
		"git status",
		"go test ./...",
		"df -h",
		"journalctl -n 20",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q only reads and must be allowed, got %s: %s", line, d.Verdict, d.Reason)
		}
	}
}

// TestWritesInsideTheWorkspaceAreAllowedAndWritesOutsideAreAsked is the workspace rule, both
// directions, on the same command with a different target. Testing only one direction is how
// a policy ends up asking about everything.
func TestWritesInsideTheWorkspaceAreAllowedAndWritesOutsideAreAsked(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside-target")

	inside := testMode().DecideLine("touch "+filepath.Join(dir, "nuevo.txt"), dir)
	if inside.Verdict != Allow {
		t.Errorf("a write inside the workspace must be allowed, got %s: %s", inside.Verdict, inside.Reason)
	}
	out := testMode().DecideLine("touch "+outside, dir)
	if out.Verdict != Ask {
		t.Errorf("a write outside the workspace must be asked about, got %s: %s", out.Verdict, out.Reason)
	}
	if out.Rule != "write-outside-workspace" {
		t.Errorf("rule = %q", out.Rule)
	}
}

// TestHomePathsAreOutside: a tilde is outside by construction, and resolving it would mean
// knowing which user the agent runs as — which is exactly the thing that must not decide.
func TestHomePathsAreOutside(t *testing.T) {
	dir := t.TempDir()
	d := testMode().DecideLine("touch ~/notes.txt", dir)
	if d.Verdict != Ask {
		t.Errorf("a home path must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestMvAndCpJudgeTheirDestination: the last operand is where it lands. `cp a b` with b
// inside is work; with b outside it is a question. The source is not the point.
func TestMvAndCpJudgeTheirDestination(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "destino")

	if d := testMode().DecideLine("cp a.txt "+filepath.Join(dir, "b.txt"), dir); d.Verdict != Allow {
		t.Errorf("a copy landing inside must be allowed, got %s: %s", d.Verdict, d.Reason)
	}
	if d := testMode().DecideLine("mv a.txt "+outside, dir); d.Verdict != Ask {
		t.Errorf("a move landing outside must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestASymlinkDoesNotSmuggleAWriteOut is the reason the comparison resolves the path: a link
// inside the workspace that points outside it is the obvious way to write out of it without
// naming anything outside it.
func TestASymlinkDoesNotSmuggleAWriteOut(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "output")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	d := testMode().DecideLine("touch "+filepath.Join(link, "escrito.txt"), dir)
	if d.Verdict != Ask {
		t.Errorf("a write through a link that leaves the workspace must be asked about, got %s: %s",
			d.Verdict, d.Reason)
	}
}

// --- lines, in segments -----------------------------------------------------

// TestAPipeIsJudgedByEveryPart: the second half of a line is where the damage goes, and a
// policy that reads the first word would let it through.
func TestAPipeIsJudgedByEveryPart(t *testing.T) {
	dir := t.TempDir()

	if d := testMode().DecideLine("cat access.log | grep 500 | wc -l", dir); d.Verdict != Allow {
		t.Errorf("a pipeline of readers must be allowed, got %s: %s", d.Verdict, d.Reason)
	}
	// A reader followed by a shell: the shell is what has to be approved.
	d := testMode().DecideLine("cat x | sh", dir)
	if d.Verdict != Ask {
		t.Errorf("a pipeline ending in a shell must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
	if d.Rule != "shell" {
		t.Errorf("rule = %q, want the shell rule", d.Rule)
	}
}

// TestAChainIsJudgedByEveryPart: the last command of a chain is the one nobody reads.
func TestAChainIsJudgedByEveryPart(t *testing.T) {
	dir := t.TempDir()

	// A local commit is work, and must not be questioned: the agent commits its own
	// validated changes, and a prompt on every `git add` would be pure friction.
	if d := testMode().DecideLine("git add -A && git commit -m x", dir); d.Verdict != Allow {
		t.Errorf("a local commit must be allowed, got %s: %s", d.Verdict, d.Reason)
	}
	// The same line with a push is a different matter: it leaves the machine.
	if d := testMode().DecideLine("git add -A && git commit -m x && git push", dir); d.Verdict != Ask {
		t.Errorf("a chain that pushes must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if d := testMode().DecideLine("true; echo x > "+outside, dir); d.Verdict != Ask {
		t.Errorf("a chain writing outside must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestTheWorstVerdictOfALineWins: one part that must be approved is enough to ask about the
// line, because the line runs as a unit.
func TestTheWorstVerdictOfALineWins(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside")
	d := testMode().DecideLine("ls && cat a && echo x > "+outside, dir)
	if d.Verdict != Ask {
		t.Errorf("the line must inherit the worst verdict, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestWritingInsideWithARedirectionIsAllowed: this is the shape the model writes for almost
// every real task, and a policy that asked about it would be turned off within a day.
func TestWritingInsideWithARedirectionIsAllowed(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"echo hi > f.txt",
		"echo hi >> f.txt",
		"printf x > " + filepath.Join(dir, "sub", "f.txt"),
		"go test ./... > resultados.txt 2>&1",
		"command -v sh > /dev/null 2>&1",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q writes inside the workspace and must be allowed, got %s: %s",
				line, d.Verdict, d.Reason)
		}
	}
}

// TestOutputToDevNullIsNotAQuestion: `> /dev/null` is the most common redirect in the world
// and it destroys nothing.
func TestOutputToDevNullIsNotAQuestion(t *testing.T) {
	dir := t.TempDir()
	d := testMode().DecideLine("ls > /dev/null", dir)
	if d.Verdict == Deny {
		t.Errorf("discarding output to /dev/null must not be refused: %s", d.Reason)
	}
}

// TestInputRedirectionOnlyReads: `<` is not a write and must not be judged as one.
func TestInputRedirectionOnlyReads(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"grep x < file.txt", "sort < datos.csv"} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q only reads and must be allowed, got %s: %s", line, d.Verdict, d.Reason)
		}
	}
}

// TestAFileDescriptorRedirectIsNotAFile: `2>&1` names a descriptor, and treating it as a path
// would ask the user about their own error plumbing.
func TestAFileDescriptorRedirectIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	d := testMode().DecideLine("make 2>&1", dir)
	if d.Verdict != Allow {
		t.Errorf("2>&1 must not be read as a file, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestAShellIsNeverRunUnreviewed: an interpreter is the one thing whose line cannot be
// classified, so it is always offered to the user, whatever the settings.
func TestAShellIsNeverRunUnreviewed(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"sh -c 'echo hi'", "bash build.sh", "zsh -i"} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Ask && d.Verdict != Deny {
			t.Errorf("%q is an interpreter and must not run unreviewed, got %s", line, d.Verdict)
		}
	}
}

// --- the two settings -------------------------------------------------------

// TestEnforceOffRunsWhatWouldBeAsked: the setting exists for a batch job with nobody at the
// keyboard, and it has to actually work or the operator is forced to edit the policy.
func TestEnforceOffRunsWhatWouldBeAsked(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside")
	m := Mode{Enforce: false}
	if d := m.DecideLine("touch "+outside, dir); d.Verdict != Allow {
		t.Errorf("with the policy off a consequential action must run, got %s: %s", d.Verdict, d.Reason)
	}
	if d := m.DecideLine("sh -c 'echo hi'", dir); d.Verdict != Allow {
		t.Errorf("with the policy off a shell must run, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestEnforceOffDoesNotWeakenStrict: the two settings are independent, and turning the
// confirmation off must not promote a refusal into a run. That direction is the one that
// would silently make a careful operator's configuration weaker than they wrote it.
func TestEnforceOffDoesNotWeakenStrict(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: false, Strict: true}
	d := m.DecideLine("totalmente-desconocido --flag", dir)
	if d.Verdict != Deny {
		t.Errorf("enforce=false must not promote a strict refusal, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestRelaxingLeavesTheFloorAlone is the one function that could break the floor, so it is
// checked directly and not only through DecideLine.
func TestRelaxingLeavesTheFloorAlone(t *testing.T) {
	floor := Decision{Verdict: Deny, Rule: "mandatory", Mandatory: true, Reason: "no"}
	for _, m := range []Mode{{}, {Enforce: true}, {Enforce: false}, {Strict: true}, {Enforce: false, Strict: true}} {
		got := m.Relaxing(floor)
		if got.Verdict != Deny {
			t.Errorf("mode %+v relaxed a mandatory refusal into %s", m, got.Verdict)
		}
	}

	// And the non-mandatory direction still works, so the function is not simply inert.
	m := Mode{Enforce: false}
	if got := m.Relaxing(Decision{Verdict: Ask, Rule: "x"}); got.Verdict != Allow {
		t.Errorf("a non-mandatory Ask must be relaxed when the policy is off, got %s", got.Verdict)
	}
	if got := (Mode{Enforce: true}).Relaxing(Decision{Verdict: Ask, Rule: "x"}); got.Verdict != Ask {
		t.Errorf("with the policy on an Ask stays an Ask, got %s", got.Verdict)
	}
}

// TestStrictRefusesWhatCannotBeClassified, and says which setting did it, so an operator
// reading the refusal knows there is a lever rather than a wall.
func TestStrictRefusesWhatCannotBeClassified(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true, Strict: true}
	d := m.DecideLine("command-nobody-knows", dir)
	if d.Verdict != Deny {
		t.Fatalf("strict must refuse the unclassified, got %s", d.Verdict)
	}
	if !strings.Contains(d.Reason, "strict") && !strings.Contains(d.Reason, "cannot classify") {
		t.Errorf("the reason must name the rule that refused it: %q", d.Reason)
	}
}

// TestAnEmptyLineIsRefused: there is nothing to run, which is a different answer from "not
// on the list" and gets its own rule.
func TestAnEmptyLineIsRefused(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"", "   ", "\t"} {
		if d := testMode().DecideLine(line, dir); d.Verdict != Deny {
			t.Errorf("%q has nothing to run and must be refused, got %s", line, d.Verdict)
		}
	}
}

// TestACommandReachingOutsideIsAsked: the workspace rule cannot see these, because there is
// no path in the arguments to compare. They get their own verdict rather than a pass.
func TestACommandReachingOutsideIsAsked(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"git push origin main",
		"git reset --hard HEAD~3",
		"git clean -fd",
		"npm publish",
		"go get github.com/x/y",
		"systemctl restart nginx",
		"curl -X DELETE https://api.example.com/x",
		"ssh host 'ls'",
		"docker run alpine",
		"sudo apt-get install vim",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Ask {
			t.Errorf("%q reaches outside the machine and must be asked about, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestLocalVerbsOfExternalToolsAreNotQuestioned is the other half of the rule above, and it
// is the half that decides whether the policy is usable. `go test`, `npm test` and `git
// status` are what working in a repository IS; asking about them would make the mechanism
// noise, and noise is what gets a guardrail switched off before it matters.
func TestLocalVerbsOfExternalToolsAreNotQuestioned(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"git status",
		"git commit -m x",
		"git add -A",
		"go test ./...",
		"go build ./...",
		"npm test",
		"npm run build",
		"cargo test",
		"docker ps",
		"kubectl get pods",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q is local work and must run, got %s (rule %s): %s", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestAWriterWithNoLocatableTargetIsNotSilentlyAllowed: `chmod` changes permissions without
// naming where the change is undone, so it cannot be called local work.
func TestAWriterWithNoLocatableTargetIsNotSilentlyAllowed(t *testing.T) {
	dir := t.TempDir()
	d := testMode().DecideLine("chmod 777 /tmp/x", dir)
	if d.Verdict == Allow {
		t.Errorf("a writer whose target cannot be placed must not run silently: %s", d.Reason)
	}
}

// TestUnclassifiedProgramsRunByDefault is the decision that keeps the policy usable, and it
// is stated as a test so that changing it is a deliberate act.
func TestUnclassifiedProgramsRunByDefault(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"make build", "npm test", "west build", "./scripts/deploy.sh"} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q is ordinary project work and must run, got %s: %s", line, d.Verdict, d.Reason)
		}
	}
}

// --- the floor and the classifier must agree --------------------------------

// TestEveryFloorProgramIsDecidedIfReachedDirectly: floorPrograms is what the line scan
// searches for, and mandatory() is what decides. A program in one and not the other is a
// hole in one direction or a lie in the other, so the two are held together here.
//
// Each program is handed the operands that make it dangerous FOR IT, because the floor is not
// "these names are always refused" — `mv a.txt b.txt` is work. What the floor refuses is the
// destructive FORM: `rm -rf /`, `mv x /`, `dd of=`.
func TestEveryFloorProgramIsDecidedIfReachedDirectly(t *testing.T) {
	dir := t.TempDir()
	for name := range floorPrograms {
		if _, hit := mandatory(name, destructiveArgs(name), dir); !hit {
			t.Errorf("%q is in floorPrograms but mandatory() does not refuse it: the scan would "+
				"catch it and the classifier would not", name)
		}
	}
}

// destructiveArgs is the dangerous form of a floor program, used by the two agreement tests
// so that both of them exercise the same shape.
func destructiveArgs(name string) []string {
	switch name {
	case "dd":
		return []string{"of=/dev/sda"}
	case "rm":
		return []string{"-rf", "/"}
	case "mv", "cp", "ln", "install":
		// The floor of these verbs is their DESTINATION, which is the last operand.
		return []string{"x", "/"}
	}
	return []string{"x"}
}

// TestTheFloorScanFindsEveryProgramTheFloorDecides: the other direction of the same
// agreement, through the entry point the agent actually uses.
func TestTheFloorScanFindsEveryProgramTheFloorDecides(t *testing.T) {
	dir := t.TempDir()
	for name := range floorPrograms {
		line := name + " " + strings.Join(destructiveArgs(name), " ")
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Deny || !d.Mandatory {
			t.Errorf("%q must be refused by the floor through DecideLine, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestTheFloorScanUnderstandsWrappersAndNesting: the ways a floor command is reached without
// being the first word of the line.
func TestTheFloorScanUnderstandsWrappersAndNesting(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"sudo rm -rf /",
		"env rm -rf /",
		"nice -n 10 rm -rf /",
		"nohup rm -rf /",
		"xargs rm -rf",
		"find . -exec rm -rf / ;",
		"FOO=bar rm -rf /",
		// A wrapper's OPTION VALUE must not be mistaken for the program: `sudo -u root`
		// names the user, and treating `root` as the command would walk past the floor.
		"sudo -u root rm -rf /",
		"sudo --user=root rm -rf /",
		"sudo -n -u root rm -rf /",
		"doas -u root rm -rf /",
		"nice -n 10 rm -rf /",
		"ionice -c 2 rm -rf /",
		"stdbuf -o0 rm -rf /",
		"env -u HOME rm -rf /",
		"time -f %e rm -rf /",
		"sudo -u root nice -n 10 rm -rf /",
	} {
		d := testMode().DecideLine(line, dir)
		if d.Verdict != Deny || !d.Mandatory {
			t.Errorf("%q must reach the floor through its wrapper, got %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}
}

// --- the workspace rule on its own ------------------------------------------

func TestFirstOutsideReportsTheFirstTargetThatLeaves(t *testing.T) {
	dir := t.TempDir()
	if got := firstOutside([]string{filepath.Join(dir, "a"), filepath.Join(dir, "b")}, dir); got != "" {
		t.Errorf("all targets are inside, got %q", got)
	}
	if got := firstOutside([]string{"", "  "}, dir); got != "" {
		t.Errorf("empty targets name nothing, got %q", got)
	}
	// A target that does not exist yet is judged by its cleaned absolute form, which is the
	// honest reading: the agent is about to create it there.
	missing := filepath.Join(filepath.Dir(dir), "todavia-no-existe")
	if got := firstOutside([]string{missing}, dir); got == "" {
		t.Error("a target outside the workspace must be reported even before it exists")
	}
	if got := firstOutside([]string{"relativo.txt"}, ""); got != "" {
		t.Errorf("with no workspace there is nothing to compare against, got %q", got)
	}
}

func TestIsRootLikeResolvesWhatItCan(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		target string
		want   bool
	}{
		{"/", true},
		{"/*", true},
		{"/home", true},
		{"/etc", true},
		{"/usr", true},
		{"/var", true},
		{"/root", true},
		{"", false},
		{filepath.Join(dir, "sub"), false},
		{"sub", false},
	}
	for _, tc := range cases {
		if got := isRootLike(tc.target, dir); got != tc.want {
			t.Errorf("isRootLike(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// TestIsRootLikeSeesTheTreeAboveTheWorkspace: deleting the directory that CONTAINS the work
// is not local work, and that is what `..` from inside the workspace means.
func TestIsRootLikeSeesTheTreeAboveTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	if !isRootLike(parent, dir) {
		t.Errorf("%q contains the workspace and must be root-like", parent)
	}
	if isRootLike(dir, dir) {
		t.Errorf("the workspace itself is where the work is")
	}
}

func TestRmTargetsHonoursTheTerminator(t *testing.T) {
	got := rmTargets([]string{"-rf", "--", "-weird-flag", "otro"})
	want := []string{"-weird-flag", "otro"}
	if len(got) != len(want) {
		t.Fatalf("rmTargets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rmTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// A single dash is stdin, not an option, and must survive as an operand.
	if got := rmTargets([]string{"-"}); len(got) != 1 || got[0] != "-" {
		t.Errorf("a lone dash is an operand, got %#v", got)
	}
}

func TestHasArgWithPrefix(t *testing.T) {
	if !hasArgWithPrefix([]string{"if=/dev/zero", "of=/dev/sda"}, "of=") {
		t.Error("the operand was there")
	}
	if hasArgWithPrefix([]string{"if=/dev/zero"}, "of=") {
		t.Error("no of= operand")
	}
}

func TestBaseNameHandlesPathsAndCase(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/RM": "rm",
		"RM":          "rm",
		"  rm  ":      "rm",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSegmentationOfTheAwkwardShapes(t *testing.T) {
	cases := []struct {
		line     string
		commands int
		redirect int
	}{
		{"ls", 1, 0},
		{"echo a > f.txt", 1, 1},
		{"echo a >> f.txt", 1, 1},
		{"cat x | wc -l", 2, 0},
		{"a && b", 2, 0},
		{"a ; b", 2, 0},
		{"make 2>&1", 1, 1},
		{"grep x < in.txt", 1, 1},
		{"", 0, 0},
		{"   ", 0, 0},
		{`echo "no | pipe here"`, 1, 0},
		{`grep -n "two words" f | wc -l`, 2, 0},
		{"echo hi > f more", 1, 1},
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
			t.Errorf("lineSegments(%q) = %d commands and %d redirects, want %d and %d (%#v)",
				tc.line, commands, redirects, tc.commands, tc.redirect, segs)
		}
	}
}

// TestARedirectKeepsTheWordsAroundIt: `echo hi > f more` is ONE command with two arguments
// and a redirection. Splitting it into two commands would classify `more` as a program.
func TestARedirectKeepsTheWordsAroundIt(t *testing.T) {
	segs := lineSegments("echo hi > f more")
	var words []string
	for _, s := range segs {
		if !s.redirect {
			words = append(words, s.words...)
		}
	}
	if len(words) != 3 {
		t.Fatalf("the command must keep every word, got %#v", words)
	}
	if words[0] != "echo" || words[1] != "hi" || words[2] != "more" {
		t.Errorf("words = %#v", words)
	}
}

func TestIsDigits(t *testing.T) {
	cases := map[string]bool{"": false, "2": true, "10": true, "x": false, "2a": false}
	for in, want := range cases {
		if got := isDigits(in); got != want {
			t.Errorf("isDigits(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLineSegmentsKeepQuotedArgumentsWhole(t *testing.T) {
	segs := lineSegments(`grep -n "two words" f`)
	if len(segs) != 1 {
		t.Fatalf("one command, got %#v", segs)
	}
	// Re-reading the segment has to see the quoted argument as ONE argument, which is what
	// a shell would do and what the program expects.
	joined := strings.Join(segs[0].words, " ")
	if !strings.Contains(joined, `"two words"`) {
		t.Errorf("the quotes must survive segmentation so the argument stays whole: %q", joined)
	}
}
