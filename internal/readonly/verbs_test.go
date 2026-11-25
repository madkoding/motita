package readonly

import "testing"

// TestVerbSkipsTheValueOfAnOption: the subcommand is the first word that is not an option and not
// the value of one, and an option nobody listed makes the answer unknown rather than a guess.
func TestVerbSkipsTheValueOfAnOption(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		verb  string
		known bool
	}{
		{"kubectl", []string{"-n", "get", "delete", "pod"}, "delete", true},
		{"kubectl", []string{"-s", "https://h", "get"}, "get", true},
		{"go", []string{"-C", "list", "run", "."}, "run", true},
		{"npm", []string{"--prefix", "bin", "exec", "-c", "x"}, "exec", true},
		{"systemctl", []string{"-p", "status", "stop", "x"}, "stop", true},
		{"systemctl", []string{"--user", "--no-pager", "status"}, "status", true},
		{"docker", []string{"--log-level=info", "ps"}, "ps", true},
		{"go", []string{"--unlisted", "x", "test"}, "", false},
		{"go", nil, "", true},
		{"go", []string{"-v"}, "", true},
	} {
		verb, known := Verb(tc.name, tc.args)
		if verb != tc.verb || known != tc.known {
			t.Errorf("Verb(%s, %v) = %q, %v; want %q, %v", tc.name, tc.args, verb, known, tc.verb, tc.known)
		}
	}
	if verbOf("go", []string{"--unlisted", "x"}) != unplacedVerb || verbOf("go", []string{"test"}) != "test" {
		t.Error("verbOf must name an unplaceable subcommand, and pass a placed one")
	}
}

// TestReadOnlyDoesNotReadAnOptionValueAsTheVerb: the examples that read as harmless verbs.
func TestReadOnlyDoesNotReadAnOptionValueAsTheVerb(t *testing.T) {
	for _, line := range [][]string{
		{"npm", "--prefix", "bin", "exec", "-c", "x"}, {"go", "-C", "list", "run", "."},
		{"systemctl", "-p", "status", "stop", "x"}, {"kubectl", "-n", "get", "delete", "pod"},
		{"systemctl", "--unlisted", "status", "stop", "x"},
	} {
		if d := Check(line[0], line[1:]); d.Allowed {
			t.Errorf("%v must not be a read: %+v", line, d)
		}
	}
}
