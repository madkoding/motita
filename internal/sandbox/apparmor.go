package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// AppArmorProfilePath is where `motita sandbox apparmor install` puts the profile that lets this
// binary make the user namespaces a command without a network runs in.
const AppArmorProfilePath = "/etc/apparmor.d/motita"

// appArmorProfileName is the name the profile carries; it is also what the kernel reports as the
// confinement of a process running it (see ownProfileAllowsNamespaces).
const appArmorProfileName = "motita"

// appArmorPatternChars are the characters that make an AppArmor path a pattern, or end it: a
// binary at such a path cannot be named exactly, so it gets no profile rather than one that also
// matches other files.
const appArmorPatternChars = "*?[]{}^\"\\,"

// AppArmorProfile is the profile that lets the binary at exe create user namespaces on a system
// that takes their capabilities away from every other program (Ubuntu 24.04 and later,
// kernel.apparmor_restrict_unprivileged_userns=1). It confines nothing else: the binary stays
// unconfined, and the only thing the profile grants is `userns`.
func AppArmorProfile(exe string) (string, error) {
	if !strings.HasPrefix(exe, "/") {
		return "", fmt.Errorf("%q is not an absolute path", exe)
	}
	if strings.ContainsAny(exe, appArmorPatternChars) || strings.IndexFunc(exe, isControl) >= 0 {
		return "", errors.New("the path of the binary has characters an AppArmor profile cannot name exactly: " +
			"install motita somewhere else")
	}
	return "abi <abi/4.0>,\ninclude <tunables/global>\n\n" +
		"profile " + appArmorProfileName + ` "` + exe + `" flags=(unconfined) {` + "\n  userns,\n}\n", nil
}

// isControl reports whether r is a control character, which has no place in a profile.
func isControl(r rune) bool { return r < ' ' || r == 0x7f }
