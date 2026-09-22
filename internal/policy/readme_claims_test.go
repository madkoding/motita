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
