package gitforge

import (
	"slices"
	"testing"
)

// A tracker resumed from the IDs an earlier one saw reports what was said since, instead of taking
// a new baseline that would swallow the comments that came in while nobody was watching.
func TestATrackerResumedFromItsSeenIDsReportsWhatCameSince(t *testing.T) {
	var first Tracker
	if first.Seen() != nil {
		t.Fatal("a tracker that has not read anything has no baseline to keep")
	}
	first.NewComments(Activity{Comments: []Comment{{ID: "c:2"}, {ID: "c:1"}}})
	seen := first.Seen()
	if !slices.Equal(seen, []string{"c:1", "c:2"}) {
		t.Fatalf("Seen = %v, want the sorted IDs", seen)
	}

	var next Tracker
	next.Resume(seen)
	news := next.NewComments(Activity{Comments: []Comment{{ID: "c:1"}, {ID: "c:2"}, {ID: "c:3"}}})
	if len(news) != 1 || news[0].ID != "c:3" {
		t.Fatalf("news = %v, want only the comment made since", news)
	}
}

// An empty baseline is still a baseline: Seen says so with an empty, non-nil list, and resuming
// from it reports every comment as news.
func TestAnEmptyBaselineIsKept(t *testing.T) {
	var first Tracker
	first.NewComments(Activity{})
	seen := first.Seen()
	if seen == nil || len(seen) != 0 {
		t.Fatalf("Seen = %#v, want an empty non-nil list", seen)
	}
	var next Tracker
	next.Resume(seen)
	if news := next.NewComments(Activity{Comments: []Comment{{ID: "c:1"}}}); len(news) != 1 {
		t.Fatalf("news = %v, want the comment made since", news)
	}
}

// Resuming from nothing leaves the tracker to take its baseline as usual.
func TestResumeFromNothingTakesTheBaseline(t *testing.T) {
	var tr Tracker
	tr.Resume(nil)
	if news := tr.NewComments(Activity{Comments: []Comment{{ID: "c:1"}}}); len(news) != 0 {
		t.Fatalf("news = %v, want the first reading taken as the baseline", news)
	}
}
