package plan

// The measurement this file exists for, taken from a real gateway log:
//
//	search_skills : 14
//	read_skill    :  2
//	save_skill    :  0
//
// Seven searches for every read. A session asked to enable a feature in a project searched the
// library, saw the summaries, and stopped — never reading a single document and never looking
// at the workspace, while the anchor declared the task complete. The summaries are what the
// model acts on when nothing tells it that a summary is not a procedure.
//
// So the wording IS the fix, and these tests are about the wording: a tool result that says
// "read one in full with read_skill" describes an OPTION, and it was read as one.

import (
	"strings"
	"testing"
)

// TestASearchResultMakesTheReadMandatory: the result of a search must not read as a menu. It
// has to say that the summaries are not the procedure and that leaving without reading one is
// the mistake this tool exists to prevent.
func TestASearchResultMakesTheReadMandatory(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{
		"nrf-build": "# NRF build\n\nUse west to produce a UF2 image for the board.\n",
	}))

	got := p.toolSearchSkills(raw(`{"query":"flash a board over usb"}`))

	// The names and the route to the full text are still there — this is an addition, not a
	// replacement of what already worked.
	for _, want := range []string{"nrf-build", "read_skill"} {
		if !strings.Contains(got, want) {
			t.Errorf("the result must still carry %q:\n%s", want, got)
		}
	}

	// And now the obligation, which is the part the measurement says was missing. The case
	// differs from the prose ("BEFORE you act"), so the check is case-insensitive: asserting
	// the exact casing would make this test fail on a copy-edit, which is not the property
	// being protected.
	lowered := strings.ToLower(got)
	for _, want := range []string{
		"summary above is not the procedure",
		"before you act",
	} {
		if !strings.Contains(lowered, strings.ToLower(want)) {
			t.Errorf("a search result must make the read obligatory, missing %q:\n%s", want, got)
		}
	}
}

// TestASearchThatMatchesNothingStillPointsForward: the miss was already right and must stay
// right. It is asserted here because the text above it is being edited, and a miss that turns
// into a dead end would cost the turn it was meant to save.
func TestASearchThatMatchesNothingStillPointsForward(t *testing.T) {
	p := (&Planner{}).WithLibrary(libOf(t, map[string]string{
		"nrf-build": "# NRF build\n\nUse west.\n",
	}))

	got := p.toolSearchSkills(raw(`{"query":"kubernetes operators on a cluster"}`))
	if !strings.Contains(got, "No skill matches") {
		t.Errorf("a miss must be stated plainly:\n%s", got)
	}
	if !strings.Contains(got, "knowledge") {
		t.Errorf("a miss must send the model to its own knowledge:\n%s", got)
	}
}
