package gateway

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// A project edited while it is being deleted ends deleted, whichever request wins, and every
// answer is one of the two honest ones (the edit applied or the project gone). Run with -race.
func TestEditingAProjectWhileItIsDeletedEndsDeleted(t *testing.T) {
	srv, _ := projectServer(t)
	for round := 0; round < 6; round++ {
		id := makeProject(t, srv, fmt.Sprintf("racing-%d", round))
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				body := fmt.Sprintf(`{"title":"edit %d","description":"d%d"}`, i, i)
				w := send(t, srv, http.MethodPatch, "/v1/projects/"+id, testToken, body)
				if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
					t.Errorf("edit answered %d: %s", w.Code, w.Body.String())
				}
			}(i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := send(t, srv, http.MethodDelete, "/v1/projects/"+id, testToken, "")
			if w.Code != http.StatusNoContent {
				t.Errorf("delete answered %d: %s", w.Code, w.Body.String())
			}
		}()
		wg.Wait()
		if srv.projectOf(id) != nil {
			t.Fatalf("round %d: the project survived its deletion", round)
		}
	}
}
