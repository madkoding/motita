package policy

// elevatingPrograms gain privileges when they start: set-user-ID programs and the ones that hold
// file capabilities. The sandbox's confinement sets NO_NEW_PRIVS, which keeps them from gaining
// any, so a line that runs one cannot run confined at all.
var elevatingPrograms = map[string]bool{
	"sudo": true, "su": true, "doas": true, "pkexec": true, "run0": true,
	"passwd": true, "chsh": true, "chfn": true, "newgrp": true, "gpasswd": true,
	"mount": true, "umount": true, "fusermount": true, "fusermount3": true,
	"crontab": true, "ping": true, "ping6": true,
}

// Elevates reports whether a line runs a program that gains privileges when it starts.
//
// Every word of the line is looked at, not only the programs it runs: a word that merely names
// one (`git commit -m "drop sudo"`) answers yes, and that only costs the line the confinement an
// approved command had never had before, never a command that would not run.
func Elevates(line string) bool {
	for _, token := range lineTokens(line) {
		if elevatingPrograms[baseName(token)] {
			return true
		}
	}
	return false
}
