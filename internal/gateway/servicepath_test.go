package gateway

import (
	"path/filepath"
	"testing"
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
