package reward

import (
	"sync"
	"testing"
	"time"
)

// TestTheLedgerIsSafeToReadWhileAVerdictIsRecorded: a library search reads a skill's value on
// every turn, and a verdict rewrites the map. Those are two different requests, so they overlap
// whenever two front ends are in use, and an unguarded map is not a race that "usually works":
// the runtime aborts the process the moment it catches a concurrent map read and map write,
// taking every other conversation with it.
//
// Run with -race. Without the mutex this fails with "WARNING: DATA RACE" or with the runtime's
// fatal "concurrent map read and map write".
func TestTheLedgerIsSafeToReadWhileAVerdictIsRecorded(t *testing.T) {
	l := &Ledger{
		Scores: map[string]Score{},
		Now:    func() time.Time { return time.Unix(0, 0) },
	}

	var wg sync.WaitGroup

	// The writer: a verdict arriving from a second front end.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = l.Attribute([]string{"count-files"}, map[string]int{"count-files": 1}, true, "")
			_ = l.Addressed("count-files")
		}
	}()

	// The readers: the library ranking a skill by what has worked, and the report that prints it.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = l.Value("count-files")
				_ = l.ValueOf("count-files")
				_, _ = l.Get("count-files")
				_ = l.Outstanding()
				_ = l.Sorted()
			}
		}()
	}

	wg.Wait()
}

// TestTheLedgerIsSafeToSaveWhileItIsBeingWritten: Save creates a temp file and renames it. Two
// saves interleaving there leave the ledger in a state neither caller intended.
func TestTheLedgerIsSafeToSaveWhileItIsBeingWritten(t *testing.T) {
	path := t.TempDir() + "/scores.json"
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = l.Attribute([]string{"count-files"}, map[string]int{"count-files": 1}, true, "")
				if err := l.Save(); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// The ledger must still be loadable: a lost update is acceptable here, a corrupt file is not.
	if _, err := Open(path); err != nil {
		t.Fatalf("the ledger the concurrent saves produced cannot be read back: %v", err)
	}
}
