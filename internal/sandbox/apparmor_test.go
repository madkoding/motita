package sandbox

import (
	"strings"
	"testing"
)

// The profile names the binary exactly and grants only userns; a path that cannot be named
// exactly gets none.
func TestAppArmorProfileNamesTheBinaryAndNothingElse(t *testing.T) {
	got, err := AppArmorProfile("/opt/my tools/motita")
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{`profile motita "/opt/my tools/motita" flags=(unconfined) {`, "  userns,\n", "abi <abi/4.0>,"}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("profile lacks %q:\n%s", want, got)
		}
	}
	bads := []string{
		"motita", "./motita", "/opt/*/motita", "/opt/mo\"tita", "/opt/mo{tita}", "/opt/mo\ntita", "/opt/mo\x7ftita",
	}
	for _, bad := range bads {
		if _, err := AppArmorProfile(bad); err == nil {
			t.Errorf("a profile was made for %q", bad)
		}
	}
}
