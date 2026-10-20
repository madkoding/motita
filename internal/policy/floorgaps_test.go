package policy

import "testing"

// The floor is the only guard when the policy is off, so every way sh has of reaching a command
// position must reach it here too: a newline, a shell's -c inside an option bundle, `exec` and
// `eval`, a backslash or quotes in the program's name, and the keywords of a compound command.
func TestTheFloorHoldsOnEveryCommandPosition(t *testing.T) {
	for _, line := range []string{
		"ls\nrm -rf /",
		"ls\r\nrm -rf /",
		"ls\nmkfs.ext4 /dev/sda",
		"bash -lc 'rm -rf /'",
		"sh -ec 'rm -rf /'",
		"bash -l -c 'rm -rf /'",
		"bash --norc -c 'rm -rf /'",
		`\rm -rf /`,
		`r''m -rf /`,
		`"rm" -rf /`,
		"exec rm -rf /",
		"eval rm -rf /",
		"eval 'rm -rf /'",
		"if true; then rm -rf /; fi",
		"while true; do rm -rf /; done",
		"! rm -rf /",
		"exec -a name rm -rf /",
		"builtin eval rm -rf /",
		"busybox rm -rf /",
		"sh -c 'ls\nrm -rf /'",
		"sh <<'EOF'\nrm -rf /\nEOF",
		"cat <<'EOF' | bash\nrm -rf /\nEOF",
	} {
		for _, m := range []Mode{{}, {Enforce: true}} {
			if d := m.DecideLine(line, "/tmp/ws"); d.Verdict != Deny || !d.Mandatory {
				t.Errorf("%+v %q = %v (%s), want a mandatory deny", m, line, d.Verdict, d.Rule)
			}
		}
	}
	// A line that only mentions the command, or a shell with no -c, is still not refused. Nor is
	// a newline inside a quote or a heredoc body that no shell runs.
	for _, line := range []string{
		"echo rm -rf /",
		"bash script.sh",
		"git commit -m 'drop rm -rf / from the docs'",
		"git commit -m \"feat: x\n\nReboot the board after flashing.\"",
		"echo 'a\nshutdown'",
		"cat > a.py <<'EOF'\ninit()\nEOF",
		"python3 - <<'EOF'\ninit()\nEOF",
	} {
		if d := (Mode{}).DecideLine(line, "/tmp/ws"); d.Mandatory {
			t.Errorf("%q was refused by the floor (%s)", line, d.Reason)
		}
	}
}
