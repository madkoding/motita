package gateway

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/memory"
)

// REORGANIZING THE MEMORY, as the Memory window's button runs it: one job at a time, in the
// background, with its progress read by GET, a stop by DELETE, and an undo that puts the copy taken
// at its start back. The model is the gateway's own small one (the summary model of the models
// block); without one the job still joins the old project lessons and associates close entries.

// reorgTimeout bounds a whole reorganization.
const reorgTimeout = 30 * time.Minute

// reorgStatus is what GET /v1/memory/reorganize answers.
type reorgStatus struct {
	Running  bool                `json:"running"`
	Progress memory.Progress     `json:"progress"`
	Report   *memory.ReorgReport `json:"report,omitempty"`
	Error    string              `json:"error,omitempty"`
}

// reorgJob is the reorganization this gateway runs or last ran.
type reorgJob struct {
	mu     sync.Mutex
	status reorgStatus
	cancel context.CancelFunc
	done   chan struct{}
}

// snapshot is the job's status as it is now.
func (j *reorgJob) snapshot() reorgStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// backupRoot is where the copies of the memory are kept: beside it, never inside it.
func (s *Server) backupRoot() string {
	return filepath.Join(filepath.Dir(filepath.Clean(s.opts.MemoryDir)), "memory-backups")
}

// reorgJudge is the gateway's model call, nil when the service has none.
func (s *Server) reorgJudge() memory.Judge {
	m, ok := s.opts.Service.(intentModel)
	if !ok {
		return nil
	}
	return m.CompleteIntent
}

// handleStartReorganize starts a reorganization, unless one is running.
func (s *Server) handleStartReorganize(w http.ResponseWriter, _ *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	j := &s.reorg
	j.mu.Lock()
	if j.status.Running {
		j.mu.Unlock()
		writeError(w, http.StatusConflict, "the memory is already being reorganized")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reorgTimeout)
	j.status = reorgStatus{Running: true, Progress: memory.Progress{Step: memory.StepBackup}}
	j.cancel = cancel
	j.done = make(chan struct{})
	done := j.done
	j.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		report, err := m.Reorganize(ctx, s.backupRoot(), s.reorgJudge(), func(p memory.Progress) {
			j.mu.Lock()
			j.status.Progress = p
			j.mu.Unlock()
		})
		j.mu.Lock()
		defer j.mu.Unlock()
		j.status.Running = false
		j.status.Report = &report
		if err != nil {
			j.status.Error = err.Error()
			s.warn("the memory's reorganization stopped", "error", err.Error())
		}
	}()
	writeJSON(w, http.StatusAccepted, j.snapshot())
}

// handleReorganizeStatus answers how the reorganization goes, or how the last one ended.
func (s *Server) handleReorganizeStatus(w http.ResponseWriter, _ *http.Request) {
	if s.memory(w) == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.reorg.snapshot())
}

// handleCancelReorganize stops the running reorganization and waits for it to stop: what it did
// stays, and the undo takes it back.
func (s *Server) handleCancelReorganize(w http.ResponseWriter, _ *http.Request) {
	if s.memory(w) == nil {
		return
	}
	j := &s.reorg
	j.mu.Lock()
	cancel, done, running := j.cancel, j.done, j.status.Running
	j.mu.Unlock()
	if running {
		cancel()
		<-done
	}
	writeJSON(w, http.StatusOK, j.snapshot())
}

// reorgUndo is what POST /v1/memory/reorganize/undo takes: the copy to put back.
type reorgUndo struct {
	Backup string `json:"backup"`
}

// handleUndoReorganize puts the memory back as the copy taken before a reorganization holds it.
func (s *Server) handleUndoReorganize(w http.ResponseWriter, r *http.Request) {
	m := s.memory(w)
	if m == nil {
		return
	}
	var b reorgUndo
	if !s.decodeBody(w, r, &b) {
		return
	}
	if s.reorg.snapshot().Running {
		writeError(w, http.StatusConflict, "the memory is being reorganized; stop it first")
		return
	}
	if err := m.Restore(s.backupRoot(), b.Backup); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, memory.ErrBadBackup) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	j := &s.reorg
	j.mu.Lock()
	j.status = reorgStatus{}
	j.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"restored": true})
}
