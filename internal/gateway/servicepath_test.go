package gateway

import (
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// The service file lives under the motita home, beside the token and the workspace, so the
// whole of the program's state is one folder the user can find, back up or delete as a unit.
func TestServiceFilePathIsUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".motita", "gateway.json")
	if got := ServiceFilePath(); got != want {
		t.Fatalf("ServiceFilePath() = %q, want %q", got, want)
	}
}

// With no HOME there is no home to use, and the relative fallback keeps the old behaviour of
// writing beside the working directory. Guessing "/tmp" or "/" would put the address of a gateway
// somewhere the user did not choose, and a stripped environment - cron, a minimal container - is a
// real case rather than a hypothetical one.
func TestServiceFilePathWithoutHomeIsRelative(t *testing.T) {
	t.Setenv("HOME", "")

	got := ServiceFilePath()
	if got != "gateway.json" {
		t.Fatalf("ServiceFilePath() = %q, want the relative fallback when there is no HOME", got)
	}
	if filepath.IsAbs(got) {
		t.Fatalf("ServiceFilePath() = %q, which is absolute and cannot be, with no home", got)
	}
}

// Whitespace is not a home either: it would produce a path like "  /.motita/gateway.json".
func TestServiceFilePathIgnoresBlankHome(t *testing.T) {
	t.Setenv("HOME", "   ")

	if got := ServiceFilePath(); got != "gateway.json" {
		t.Fatalf("ServiceFilePath() = %q, want the relative fallback for a blank HOME", got)
	}
}

// The service file FOLLOWS the motita home rather than deciding for itself where that is.
//
// It used to read HOME directly and fall back to a bare relative name, which was a SECOND copy
// of the rule config.Dir() already implements - and two copies of one rule drift. They had: the
// gateway's copy knew nothing of the operating-system fallback, so on Windows (which has no
// HOME) it wrote "gateway.json" beside whatever directory the user happened to be in, while the
// rest of the program used the OS home. This asserts the one shared answer.
//
// On Linux this cannot fail before the fix as well as after, because both implementations
// compute the same string there. What it does is pin the COUPLING, so re-introducing a private
// copy of the rule is caught here rather than on a Windows machine nobody tests on.
func TestServiceFilePathFollowsTheMotitaHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := ServiceFilePath(), filepath.Join(config.Dir(), "gateway.json"); got != want {
		t.Fatalf("ServiceFilePath() = %q, want it to follow config.Dir() (%q)", got, want)
	}
	// And the answer is the same one the rest of the program's state uses, not a lookalike.
	if got, want := ServiceFilePath(), filepath.Join(home, ".motita", "gateway.json"); got != want {
		t.Fatalf("ServiceFilePath() = %q, want %q", got, want)
	}
}
