package policy

import "testing"

// TestGitConfigReadVersusWrite: `git config` reaches outside when it SETS a value or opens the
// configuration for writing, and stays local when it only reads. Writing the repository's config
// is how an agent plants a command a later git would run, so it is asked about.
func TestGitConfigReadVersusWrite(t *testing.T) {
	reads := [][]string{
		{"config", "user.name"},
		{"config", "--get", "user.name"},
		{"config", "--list"},
		{"config", "--get-regexp", "^remote"},
		{"-C", "/work", "config", "--list"},
	}
	for _, args := range reads {
		if reachesOutside("git", args) {
			t.Errorf("git %v only reads the configuration", args)
		}
	}
	writes := [][]string{
		{"config", "user.name", "Someone"},
		{"config", "filter.x.clean", "sh -c evil"},
		{"config", "--add", "safe.directory", "/x"},
		{"config", "--unset", "user.name"},
		{"config", "--replace-all", "user.name", "x"},
		{"config", "set", "user.name", "x"},
		{"config", "unset", "user.name"},
		{"config", "--edit"},
		{"config", "-e"},
		{"config", "rename-section", "a", "b"},
		{"config", "remove-section", "a"},
		{"-C", "/work", "config", "filter.x.clean", "evil"},
	}
	for _, args := range writes {
		if !reachesOutside("git", args) {
			t.Errorf("git %v writes the configuration and must be asked about", args)
		}
	}
}
