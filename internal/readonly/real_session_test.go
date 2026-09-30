package readonly

import "testing"

// This is the measurement the change was made on, kept as a test so the number cannot
// silently go back. It is not an assertion about the tool: it is a count of how many of a
// REAL session's approval requests stop being questions, and the count is what has to be
// right.
//
// The evidence is `~/.motita/gw-live.log`: 228 lines the user was asked to approve in one
// working session about a Node project. 204 of them were `writer-unclassified`, and 178 of
// those were `sed` in its printing form. If this test's number moves, the change moved it and
// the reason has to be understood, not adjusted.
func TestTheApprovalRequestsOfARealSessionAreNoLongerQuestions(t *testing.T) {
	// A representative slice of that log, in the shapes it actually used. The count here is
	// not the 228 — the log is not in the repository — but every distinct SHAPE is, and each
	// shape used to be a question.
	reading := []struct {
		command string
		args    []string
	}{
		{"sed", []string{"-n", "1,300p", "app/admin/page.tsx"}},
		{"sed", []string{"-n", "200,404p", "server/src/routes.mjs"}},
		{"sed", []string{"-n", "1,70p", "server/src/validation.mjs"}},
		{"sed", []string{"-n", "80,120p", "lib/api.ts"}},
		{"sed", []string{"-n", "1,600p", "components/admin-page.tsx"}},
		{"sed", []string{"-n", "1,241p", "server/src/db/schema.sql"}},
		{"awk", []string{"'/CREATE TABLE IF NOT EXISTS vtuber /,/^\\);/'", "server/src/db/schema.sql"}},
		{"awk", []string{"'{print $1}'", "package.json"}},
	}
	for _, tc := range reading {
		if d := Check(tc.command, tc.args); !d.Allowed {
			t.Errorf("this line stopped someone 178+12 times and must now be silent: %s %v -> %s",
				tc.command, tc.args, d.Reason)
		}
	}
	// And the writing forms of the same two programs, which must still stop the run.
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"sed", []string{"-i", "s/a/b/", "lib/fichas-nuevas.mjs"}},
		{"awk", []string{"-i", "inplace", "'{print}'", "f"}},
	} {
		if d := Check(tc.command, tc.args); d.Allowed {
			t.Errorf("the writing form of a line editor must still be refused: %s %v", tc.command, tc.args)
		}
	}
}
