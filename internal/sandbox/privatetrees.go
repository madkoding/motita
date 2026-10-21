package sandbox

import (
	"os"
	"runtime"
	"strings"
)

// goos is the platform the private trees are chosen for. A variable so the tests, which run on
// Linux, can measure what Windows gets. The file has no build tag for the same reason.
var goos = runtime.GOOS

// getenv reads the environment the private trees are taken from; a variable for the tests.
var getenv = os.Getenv

// platformPrivateTrees are the trees, besides privateTrees, where this platform keeps people's
// files. privateTrees are POSIX paths, and on Windows none of them matches anything: the user's
// profile (%USERPROFILE%, holding .ssh, .motita and the rest), the folder of every profile
// (C:\Users) and the HOME that motita's own home is under when it is set were readable by every
// read tool.
//
// On macOS people's files are under /Users, external disks under /Volumes, and /home is a link to
// /System/Volumes/Data/home: a read tool judges the resolved path, which none of the POSIX trees
// match.
func platformPrivateTrees() []string {
	switch goos {
	case "windows":
		return windowsPrivateTrees(getenv)
	case "darwin":
		return []string{"/Users", "/Volumes", "/System/Volumes/Data/home"}
	default:
		return nil
	}
}

// windowsPrivateTrees are the Windows trees that hold people's files: the profile, the folder of
// the profiles beside it, %SystemDrive%\Users, and HOME when it is set. Blank values are skipped.
func windowsPrivateTrees(env func(string) string) []string {
	var trees []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" {
			trees = append(trees, p)
		}
	}
	profile := strings.TrimSpace(env("USERPROFILE"))
	add(profile)
	add(windowsParent(profile))
	if drive := strings.TrimSpace(env("SystemDrive")); drive != "" {
		add(drive + `\Users`)
	}
	add(env("HOME"))
	return trees
}

// windowsParent is the directory above a Windows path, or "" when that would be a drive's root or
// nothing at all: `C:\Users\me` is `C:\Users`, and `C:\me` has no parent worth hiding.
func windowsParent(p string) string {
	p = strings.TrimRight(p, `\/`)
	i := strings.LastIndexAny(p, `\/`)
	if i < 0 {
		return ""
	}
	parent := p[:i]
	if len(strings.TrimRight(parent, `\/`)) <= 2 {
		return ""
	}
	return parent
}

// foldCase lower-cases a path where the file system ignores case (Windows), so `c:\users\me` is
// under `C:\Users`.
func foldCase(p string) string {
	if goos == "windows" {
		return strings.ToLower(p)
	}
	return p
}
