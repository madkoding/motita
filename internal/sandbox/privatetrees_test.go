package sandbox

import (
	"reflect"
	"testing"
)

// onPlatform measures the private trees as goos with env for one test.
func onPlatform(t *testing.T, platform string, env map[string]string) {
	t.Helper()
	oldGOOS, oldEnv := goos, getenv
	goos = platform
	getenv = func(k string) string { return env[k] }
	t.Cleanup(func() { goos, getenv = oldGOOS, oldEnv })
}

// On Windows the user's profile, the folder of every profile and HOME are private: the POSIX
// trees match nothing there, and every read tool could reach ~/.ssh and ~/.motita/auth.
func TestWindowsProfilesArePrivate(t *testing.T) {
	onPlatform(t, "windows", map[string]string{
		"USERPROFILE": `C:\Users\me`, "SystemDrive": "D:", "HOME": `E:\home\me`,
	})
	want := []string{`C:\Users\me`, `C:\Users`, `D:\Users`, `E:\home\me`}
	if got := platformPrivateTrees(); !reflect.DeepEqual(got, want) {
		t.Errorf("platformPrivateTrees() = %q, want %q", got, want)
	}
	s := &Sandbox{}
	private := s.privatePaths()
	if got := private[len(privateTrees):]; !reflect.DeepEqual(got, want) {
		t.Errorf("privatePaths() adds %q, want %q", got, want)
	}
}

func TestWindowsPrivateTreesSkipWhatIsNotSet(t *testing.T) {
	if got := windowsPrivateTrees(func(string) string { return " " }); len(got) != 0 {
		t.Errorf("an empty environment: %q", got)
	}
	got := windowsPrivateTrees(func(k string) string {
		if k == "USERPROFILE" {
			return `C:\me\`
		}
		return ""
	})
	if !reflect.DeepEqual(got, []string{`C:\me\`}) {
		t.Errorf("a profile at a drive's root: %q", got)
	}
}

func TestWindowsParent(t *testing.T) {
	for p, want := range map[string]string{
		`C:\Users\me`: `C:\Users`, `C:/Users/me/`: `C:/Users`, `C:\me`: "", "me": "", "": "",
		`\\srv\share\me`: `\\srv\share`,
	} {
		if got := windowsParent(p); got != want {
			t.Errorf("windowsParent(%q) = %q, want %q", p, got, want)
		}
	}
}

// On macOS the users' folders, the external disks and the place /home leads to are private.
func TestMacOSUsersArePrivate(t *testing.T) {
	onPlatform(t, "darwin", map[string]string{"USERPROFILE": `C:\Users\me`})
	want := []string{"/Users", "/Volumes", "/System/Volumes/Data/home"}
	if got := platformPrivateTrees(); !reflect.DeepEqual(got, want) {
		t.Errorf("platformPrivateTrees() = %q, want %q", got, want)
	}
}

// Elsewhere the POSIX trees are the whole list.
func TestOtherPlatformsAddNoPrivateTrees(t *testing.T) {
	onPlatform(t, "linux", map[string]string{"USERPROFILE": `C:\Users\me`})
	if got := platformPrivateTrees(); got != nil {
		t.Errorf("platformPrivateTrees() = %q, want none", got)
	}
}

// Windows ignores case, so a path under a private tree written in another case is still under it.
func TestPathUnderFoldsCaseOnWindows(t *testing.T) {
	onPlatform(t, "windows", nil)
	if !pathUnder("/HOME/me/.ssh", []string{"/home"}) {
		t.Error("windows: /HOME/me/.ssh must be under /home")
	}
	onPlatform(t, "linux", nil)
	if pathUnder("/HOME/me/.ssh", []string{"/home"}) {
		t.Error("linux: /HOME is not /home")
	}
}
