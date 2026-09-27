package skills

// The seam that turns a document off. A turned-off document is not gone: it keeps its file and
// its page in the interface, and what changes is only whether the MODEL is offered it. The two
// ways a skill reaches a session unasked are the index and the search, and the search reads the
// index, so the filter lives in the one place both of them pass through.

import "testing"

// TestHiddenDocumentsLeaveTheIndexAndTheSearch: the index and the search are how a skill arrives
// without anyone asking for it by name, and a user who turned one off wants exactly that to stop.
// A read by name still works, because the document is still there and a caller that already knows
// the name is not being pushed anything.
func TestHiddenDocumentsLeaveTheIndexAndTheSearch(t *testing.T) {
	l := newLib(t)
	if _, err := l.Save("off-one", "# Off one\n\nquietly retired zebra\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("on-one", "# On one\n\nstill offered\n"); err != nil {
		t.Fatal(err)
	}
	l.Hidden = func(name string) bool { return name == "off-one" }

	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Name != "on-one" {
		t.Errorf("List = %v, want only the document that was not turned off", all)
	}

	// A word that appears only in the body of the turned-off document: if the search still
	// answers with it, the filter is not covering the way the model actually reaches a skill.
	hits, err := l.Search("zebra", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("Search found %d hits for a word only the turned-off document contains", len(hits))
	}

	got, err := l.Get("off-one")
	if err != nil {
		t.Fatalf("a turned-off document must still be readable by name: %v", err)
	}
	if got.Name != "off-one" {
		t.Errorf("Get = %q, want the document that was turned off", got.Name)
	}

	// A nil seam is "yes to everything", which is the behaviour from before the seam existed.
	l.Hidden = nil
	all, err = l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List with the seam off = %v, want both documents", all)
	}
}

// TestHiddenAlsoFiltersTheBuiltins: the interface that turns a document off has no idea which of
// the two libraries it is showing, so a shipped procedure has to be turned off by the same rule.
// A filter applied only to the directory would leave the model reading a procedure the user
// removed from view.
func TestHiddenAlsoFiltersTheBuiltins(t *testing.T) {
	l := builtinLib(t)
	// A document of the session's own, so the listing after the filter still has something in
	// it: the binary may ship a single procedure, and a check against an empty index would pass
	// without asserting anything.
	if _, err := l.Save("mine", "# Mine\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	shipped := false
	for _, s := range all {
		if s.Name == "files-and-directories" {
			shipped = true
		}
	}
	if !shipped {
		t.Fatal("the shipped procedure must be listed before it is turned off")
	}

	l.Hidden = func(name string) bool { return name == "files-and-directories" }
	all, err = l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Name != "mine" {
		t.Errorf("List = %v, want only the document that was not turned off", all)
	}
}
