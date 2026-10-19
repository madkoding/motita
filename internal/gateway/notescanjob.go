package gateway

import (
	"context"
	"net/http"
	"sync"
)

// A scan asked for in the background runs on its own while the page asks how far it got, so a big
// vault with the model on never holds a request open for minutes.

// scanProgress is what GET /v1/notes/scan answers: whether a scan runs, where it is and, once it
// ended, what it did or why it failed.
type scanProgress struct {
	Running bool `json:"running"`
	// Phase is "sessions" while the session notes are written, then "projects".
	Phase string `json:"phase,omitempty"`
	// Project is the project being mapped; Done of Total projects are finished.
	Project string `json:"project,omitempty"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	// Chapters of the project's manual written so far, of ChapterTotal.
	Chapters     int       `json:"chapters"`
	ChapterTotal int       `json:"chapter_total"`
	Result       *noteScan `json:"result,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// scanTracker holds the progress of the one background scan. A nil tracker records nothing, which
// is what a scan answered in the request itself uses.
type scanTracker struct {
	mu sync.Mutex
	p  scanProgress
}

func (t *scanTracker) update(fn func(p *scanProgress)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.p)
}

func (t *scanTracker) get() scanProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}

// start marks a scan as running, unless one already is.
func (t *scanTracker) start() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.p.Running {
		return false
	}
	t.p = scanProgress{Running: true, Phase: "sessions"}
	return true
}

func (t *scanTracker) finish(res noteScan, err error) {
	t.update(func(p *scanProgress) {
		*p = scanProgress{Done: p.Total, Total: p.Total}
		if err != nil {
			p.Error = err.Error()
			return
		}
		p.Result = &res
	})
}

// handleScanStatus answers how far the background scan got.
func (s *Server) handleScanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.scanJob.get())
}

// startScan runs a scan in the background, unless one already runs, and answers its progress.
func (s *Server) startScan(w http.ResponseWriter, run func(ctx context.Context, t *scanTracker) (noteScan, error)) {
	if s.scanJob.start() {
		go func() {
			res, err := run(context.Background(), &s.scanJob)
			s.scanJob.finish(res, err)
		}()
	}
	writeJSON(w, http.StatusAccepted, s.scanJob.get())
}
