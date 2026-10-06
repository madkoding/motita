package agent

import "testing"

func TestGoalOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/goal add a login page", "add a login page", true},
		{"  /goal   fix the build ", "fix the build", true},
		{"/goal", "/goal", false},
		{"/goalpost review", "/goalpost review", false},
		{"fix the build", "fix the build", false},
	}
	for _, c := range cases {
		got, ok := goalOf(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("goalOf(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
