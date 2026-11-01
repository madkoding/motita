package readonly

import (
	"slices"
	"testing"
)

// TestSedInPlaceNamesTheFilesItRewrites: an in-place edit whose script only substitutes, deletes
// or prints is read for the files it rewrites, however the in-place option is spelled.
func TestSedInPlaceNamesTheFilesItRewrites(t *testing.T) {
	cases := []struct {
		args  []string
		files []string
	}{
		{[]string{"-i", "s/a/b/", "f.go"}, []string{"f.go"}},
		{[]string{"-i.bak", "s/a/b/g", "f.go", "g.go"}, []string{"f.go", "g.go"}},
		{[]string{"-ni", "/x/p", "f"}, []string{"f"}},
		{[]string{"-ie", "s/a/b/", "f"}, []string{"f"}},
		{[]string{"--in-place", "-E", "s/(a)/b/", "f"}, []string{"f"}},
		{[]string{"--in-place=.orig", "/^#/d", "f"}, []string{"f"}},
		{[]string{"-i", "-e", "s/a/b/", "-e", "s/c/d/", "f"}, []string{"f"}},
		{[]string{"-i", "-es/a/b/", "f"}, []string{"f"}},
		{[]string{"-i", "-l", "80", "s/a/b/", "f"}, []string{"f"}},
	}
	for _, c := range cases {
		files, ok := SedInPlace(c.args)
		if !ok || !slices.Equal(files, c.files) {
			t.Errorf("sed %q: got %q, %v; want %q", c.args, files, ok, c.files)
		}
	}
}

// TestSedInPlaceLeavesTheRestToTheQuestion: what writes or runs something besides the file, what
// cannot be read, and what is not an in-place edit at all is not answered.
func TestSedInPlaceLeavesTheRestToTheQuestion(t *testing.T) {
	for _, args := range [][]string{
		{"-n", "1,5p", "f"},                        // not in place
		{"-i", "s/a/b/w /tmp/x", "f"},              // writes another file
		{"-i", "1e id", "f"},                       // runs a command
		{"-i", "s/a/b/"},                           // no file
		{"-i", "-e", "s/a/b/"},                     // no file either
		{"-f", "x.sed", "-i", "f"},                 // a script nobody can see
		{"--follow-symlinks", "-i", "s/a/b/", "f"}, // writes through a link
		{"-i../x", "s/a/b/", "f"},                  // backup in another directory
		{"-i*", "s/a/b/", "f"},                     // backup named after the file elsewhere
		{"--in-place=/tmp/", "s/a/b/", "f"},        // the same, long
		{"-i", "../../etc/x", "f"},                 // BSD reads it as the suffix
		{"-i", "", "s/a/b/", "f"},                  // GNU and BSD disagree
		{"-i", "--", "s/a/b/", "f"},                // past the options
		{"-i", "--unknown", "s/a/b/", "f"},         // an option sed does not have
	} {
		if files, ok := SedInPlace(args); ok {
			t.Errorf("sed %q was answered with %q", args, files)
		}
	}
}
