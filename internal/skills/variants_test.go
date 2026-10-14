package skills

import (
	"strings"
	"testing"
)

// onPlatform serves the embedded set of goos for the rest of the test.
func onPlatform(t *testing.T, goos string) {
	t.Helper()
	old := builtinGOOS
	builtinGOOS = goos
	t.Cleanup(func() { builtinGOOS = old })
}

// The procedures that are all commands ship a variant per platform: on Windows the same name
// serves the PowerShell document, and anywhere else the variant is not served at all.
func TestAPlatformVariantReplacesTheProcedureOnItsPlatformOnly(t *testing.T) {
	for _, name := range []string{fileSkill, curlSkill} {
		onPlatform(t, "windows")
		s, err := builtinLib(t).Get(name)
		if err != nil {
			t.Fatalf("windows %s: %v", name, err)
		}
		if s.Path != "builtin:"+name+".windows.md" || !strings.Contains(s.Body, "PowerShell") {
			t.Errorf("windows %s served %s", name, s.Path)
		}

		onPlatform(t, "linux")
		s, err = builtinLib(t).Get(name)
		if err != nil {
			t.Fatalf("linux %s: %v", name, err)
		}
		if s.Path != "builtin:"+name+".md" || strings.Contains(s.Body, "Get-ChildItem") {
			t.Errorf("linux %s served %s", name, s.Path)
		}
	}
}

// A variant is never a document of its own, and never adds one: every variant names a procedure
// that exists for every platform, so no platform is missing a name another one has.
func TestAVariantIsNeverADocumentOfItsOwn(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		onPlatform(t, goos)
		all, err := builtinSkills()
		if err != nil {
			t.Fatal(err)
		}
		onPlatform(t, "plan9")
		base, err := builtinSkills()
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != len(base) {
			t.Errorf("%s serves %d procedures, the base set has %d", goos, len(all), len(base))
		}
		for name := range all {
			if strings.Contains(name, ".") {
				t.Errorf("%s serves %q, a variant under its file name", goos, name)
			}
			if _, ok := base[name]; !ok {
				t.Errorf("%s serves %q, which no other platform has", goos, name)
			}
		}
	}
}

// The Windows set has to pass what the shipped set passes: a title and a summary for every
// document, and the words of the job finding the right one.
func TestTheWindowsProceduresHoldUpLikeTheOthers(t *testing.T) {
	onPlatform(t, "windows")
	t.Run("load", TestTheShippedProceduresLoad)
	t.Run("found", TestTheShippedProceduresAreFoundByTheWordsOfTheJob)
}
