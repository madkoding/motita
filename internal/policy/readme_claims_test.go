package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// This test exists only to CHECK the README's claims about the floor against the real
// classifier: every sentence in the README that states which lines are refused is asserted
// here, so the documentation cannot drift away from the behaviour.
//
// The destructive lines are assembled from parts so that this file does not itself contain
// a runnable-looking one.
func TestTheREADMEsClaimsAboutTheFloorHold(t *testing.T) {
	rmRF := "rm -rf" + " /"
	shC := "sh -c '" + rmRF + "'"
	mention := "echo \"" + rmRF + "\""

	cases := []struct {
		line string
		want bool
		desc string
	}{
		{rmRF, true, "README: rm in command position is refused"},
		{shC, true, "README: caught as the payload of <shell> -c"},
		{"sudo " + rmRF, true, "README: after a wrapper like sudo"},
		{"echo x | xargs " + rmRF, true, "README: after xargs"},
		{"find / -exec " + rmRF + " {} \\;", true, "README: after find -exec"},
		{"dd if=/dev/zero of=/dev/" + "sda", true, "README: dd targeting a device"},
		{"mkfs.ext4 /dev/" + "sda1", true, "README: the mkfs.* family"},
		{"shutdown" + " -h now", true, "README: the machine's power state"},
		{"wipefs -a /dev/" + "sda", true, "README: wipefs"},
		{mention, false, "README: a line that merely MENTIONS it is not refused"},
		{"ls -la", false, "README: a plain read is untouched"},
		{"grep -rn pattern .", false, "README: grep is allowed"},
		// The README says dd is refused whenever it names an OUTPUT, so both of these are
		// refused and the read-only form is not. This is the claim an operator is most
		// likely to disbelieve, which is why it is written down.
		{"dd if=/dev/zero of=local.img bs=1M count=1", true, "README: dd of=local.img is refused even though it is a local file"},
		{"dd if=image.img", false, "README: a dd that only READS is untouched"},
		// And the README says fdisk -l, which only lists, is refused with its family.
		{"fdisk -l", true, "README: fdisk -l is refused along with the rest of its family"},
		{"lsblk", false, "README: lsblk is offered as the way to inspect a partition table"},
		// Refused BY TARGET: the same verb on an ordinary path is not the floor's business.
		{"cp file.txt /tmp/x", false, "README: cp against ordinary paths is not the floor's business"},
		{"install -m 755 bin /tmp/x", false, "README: install against ordinary paths is not the floor's business"},
	}

	for _, c := range cases {
		d, hit := MandatoryInLine(c.line, t.TempDir())
		if hit != c.want {
			t.Errorf("README is WRONG about %s: floor=%v, documented=%v (rule %q)",
				c.desc, hit, c.want, d.Rule)
			continue
		}
		t.Logf("holds: %-58s refused=%-5v", c.desc, hit)
	}
}

// The README must not claim more than the code does, and the subtlety here is WHICH tree is
// protected: the directory that CONTAINS the work, not the work. Getting this backwards in
// prose is how an operator concludes the agent cannot touch their project at all.
func TestTheREADMEsWorkingTreeClaimHolds(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		line string
		want bool
		desc string
	}{
		{"rm -rf " + parent, true, "README: the tree ABOVE the workspace is protected"},
		{"rm -rf " + workspace, false, "README: the workspace itself is local work, not the floor"},
		{"rm -rf " + filepath.Join(workspace, "sub"), false, "README: a path inside the workspace is local work"},
		{"mv " + parent + " /tmp/x", true, "README: mv reaches the same tree from another direction"},
	}
	for _, c := range cases {
		d, hit := MandatoryInLine(c.line, workspace)
		if hit != c.want {
			t.Errorf("README is WRONG about %s: floor=%v, documented=%v (rule %q)",
				c.desc, hit, c.want, d.Rule)
			continue
		}
		t.Logf("holds: %-58s refused=%-5v", c.desc, hit)
	}
}

// The guardrail section diagrams three verdicts, and the diagram is the part an operator reads
// and believes. Each row of it is asserted here, in both directions: what the README says runs
// without a question must run, and what it says is asked about must be asked about.
//
// The middle row is the one that took a fix to make true, so it is the one worth pinning: the
// project's ordinary work is silent, and what nobody can predict is not.
func TestTheREADMEsGuardrailDiagramHolds(t *testing.T) {
	dir := t.TempDir()

	// "allow — it changes nothing, or it changes the workspace you pointed the agent at"
	silent := []string{
		"ls -la", "cat README.md", "grep -rn TODO .", "make", "make check",
		"go build ./...", "go test ./...", "npm test", "python3 build.py",
		"rm -rf build", "mkdir -p out",
	}
	for _, line := range silent {
		if d := testMode().DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("README says %q runs without a question; it is %s (rule %s)",
				line, d.Verdict, d.Rule)
		}
	}

	// "ask — it reaches the network or the system, writes outside the workspace, or is
	//  something nobody can classify → YOU are asked"
	asked := []string{
		"pip install x",               // reaches the network
		"git push origin main",        // reaches outside the machine
		"echo x > /tmp/outside.txt",   // writes outside the workspace
		"htop",                        // nobody can classify it
		"python3 -c 'x'",              // an inline program, as opaque as a shell line
		"python3 /opt/other/build.py", // a script from outside the workspace
	}
	for _, line := range asked {
		if d := testMode().DecideLine(line, dir); d.Verdict != Ask {
			t.Errorf("README says %q is asked about; it is %s (rule %s)", line, d.Verdict, d.Rule)
		}
	}

	// "deny — strict mode: what cannot be classified is refused instead of asked about"
	strict := Mode{Enforce: true, Strict: true}
	for _, line := range []string{"htop", "terraform apply", "chmod 644"} {
		if d := strict.DecideLine(line, dir); d.Verdict != Deny {
			t.Errorf("README says strict refuses %q; it is %s", line, d.Verdict)
		}
	}

	// "no key, no variable, no flag can reach this" — the floor, under the most permissive
	// mode there is. Enforce off and Strict off is the most the operator can loosen.
	loosest := Mode{Enforce: false, Strict: false}
	floor := "rm -rf" + " /"
	if d := loosest.DecideLine(floor, dir); !d.Mandatory || d.Verdict != Deny {
		t.Errorf("README says the floor is unreachable by configuration; %q gave %s (mandatory=%v)",
			floor, d.Verdict, d.Mandatory)
	}
}
