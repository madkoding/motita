package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoNewPrivsIn(t *testing.T) {
	cases := map[string]bool{
		"Name:\tmotita\nNoNewPrivs:\t1\nSeccomp:\t0\n": true,
		"Name:\tmotita\nNoNewPrivs:\t0\n":              false,
		"Name:\tmotita\n":                              false,
		"":                                             false,
	}
	for in, want := range cases {
		if got := noNewPrivsIn(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestNoNewPrivsReadsTheStatusFile(t *testing.T) {
	old := procStatus
	defer func() { procStatus = old }()

	file := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(file, []byte("NoNewPrivs:\t1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	procStatus = file
	if !NoNewPrivs() {
		t.Error("a status with NoNewPrivs: 1 must report the flag")
	}
	procStatus = filepath.Join(t.TempDir(), "missing")
	if NoNewPrivs() {
		t.Error("an unreadable status reports no flag")
	}
}
