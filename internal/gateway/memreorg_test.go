package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/memory"
)

// blockingModel answers the reorganization's questions only once released, or when its call is
// cancelled.
type blockingModel struct {
	*fakeService
	started chan struct{}
	release chan struct{}
}

func (m *blockingModel) CompleteIntent(ctx context.Context, _, _ string) (string, error) {
	select {
	case m.started <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
		return `[{"n":0,"type":"fact"}]`, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func reorgStatusOf(t *testing.T, srv *Server) reorgStatus {
	t.Helper()
	w := memoryCall(t, srv, http.MethodGet, "/v1/memory/reorganize", "")
	var st reorgStatus
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	return st
}

// waitReorg waits for the job to end and answers how it ended.
func waitReorg(t *testing.T, srv *Server) reorgStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := reorgStatusOf(t, srv); !st.Running {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the reorganization did not end")
	return reorgStatus{}
}

func TestTheMemoryIsReorganizedAndUndone(t *testing.T) {
	svc := &modelService{fakeService: &fakeService{}, answer: `[{"n":0,"type":"decision"}]`}
	dir := filepath.Join(t.TempDir(), "memory")
	srv := newTestServer(t, svc, func(o *Options) { o.MemoryDir = dir })
	m := memory.Open(dir)
	if _, err := m.Write("Projects/shop.md", "- make test runs the gate", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write("plan.md", "We ship on friday", false); err != nil {
		t.Fatal(err)
	}
	w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	st := waitReorg(t, srv)
	if st.Report == nil || st.Error != "" || st.Report.Joined != 1 || !st.Report.UsedModel || st.Report.Backup == "" {
		t.Fatalf("status = %+v report %+v", st, st.Report)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "memory-backups", st.Report.Backup, "plan.md")); err != nil {
		t.Errorf("the copy is kept beside the memory: %v", err)
	}
	if _, _, err := m.Read("shop/Lessons.md"); err != nil {
		t.Errorf("the lessons joined their project: %v", err)
	}
	if svc.asked == 0 {
		t.Error("the model was not asked")
	}
	w = memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize/undo", `{"backup":"`+st.Report.Backup+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", w.Code, w.Body)
	}
	if _, _, err := m.Read("Projects/shop.md"); err != nil {
		t.Errorf("the undo put the memory back: %v", err)
	}
	if st := reorgStatusOf(t, srv); st.Report != nil {
		t.Errorf("after the undo nothing is left to show: %+v", st)
	}
	w = memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize/undo", `{"backup":"20200101-000000"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("an unknown copy: %d", w.Code)
	}
	w = memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize/undo", `not json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a bad body: %d", w.Code)
	}
}

func TestAReorganizationIsStoppedAndRunsOnce(t *testing.T) {
	svc := &blockingModel{fakeService: &fakeService{}, started: make(chan struct{}, 1), release: make(chan struct{})}
	dir := filepath.Join(t.TempDir(), "memory")
	srv := newTestServer(t, svc, func(o *Options) { o.MemoryDir = dir })
	if _, err := memory.Open(dir).Write("a.md", "something to sort", false); err != nil {
		t.Fatal(err)
	}
	if w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize", ""); w.Code != http.StatusAccepted {
		t.Fatalf("start: %d", w.Code)
	}
	<-svc.started
	if w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize", ""); w.Code != http.StatusConflict {
		t.Errorf("a second start: %d", w.Code)
	}
	if w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize/undo", `{"backup":"x"}`); w.Code != http.StatusConflict {
		t.Errorf("an undo while running: %d", w.Code)
	}
	w := memoryCall(t, srv, http.MethodDelete, "/v1/memory/reorganize", "")
	var st reorgStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != http.StatusOK || st.Running || !strings.Contains(st.Error, "canceled") {
		t.Errorf("stop: %d %+v", w.Code, st)
	}
	if w := memoryCall(t, srv, http.MethodDelete, "/v1/memory/reorganize", ""); w.Code != http.StatusOK {
		t.Errorf("a stop with nothing running: %d", w.Code)
	}
}

func TestAReorganizationWithoutAModelOrMemory(t *testing.T) {
	srv, _ := memoryServer(t)
	if srv.reorgJudge() != nil {
		t.Error("a service without a model has no judge")
	}
	if w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize", ""); w.Code != http.StatusAccepted {
		t.Fatalf("start: %d", w.Code)
	}
	if st := waitReorg(t, srv); st.Report == nil || st.Report.UsedModel {
		t.Errorf("status = %+v", st)
	}
	none := newTestServer(t, &fakeService{})
	for _, call := range [][2]string{
		{http.MethodPost, "/v1/memory/reorganize"}, {http.MethodGet, "/v1/memory/reorganize"},
		{http.MethodDelete, "/v1/memory/reorganize"}, {http.MethodPost, "/v1/memory/reorganize/undo"},
	} {
		if w := memoryCall(t, none, call[0], call[1], `{}`); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s without a memory: %d", call[0], call[1], w.Code)
		}
	}
}

func TestAReorganizationThatCannotCopyTheMemory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "memory")
	_ = os.WriteFile(filepath.Join(parent, "memory-backups"), []byte("a file"), 0o600)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.MemoryDir = dir })
	memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize", "")
	if st := waitReorg(t, srv); st.Error == "" {
		t.Errorf("status = %+v", st)
	}
	// A copy that is there but cannot be put back answers a server error.
	m := memory.Open(dir)
	_ = os.Remove(filepath.Join(parent, "memory-backups"))
	if _, err := m.Write("a.md", "x", false); err != nil {
		t.Fatal(err)
	}
	name, err := m.Backup(srv.backupRoot())
	if err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(srv.backupRoot(), name, "big.md")
	_ = os.WriteFile(big, []byte(strings.Repeat("x", memory.MaxEntryBytes+1)), 0o600)
	w := memoryCall(t, srv, http.MethodPost, "/v1/memory/reorganize/undo", `{"backup":"`+name+`"}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("undo of a copy the memory cannot take: %d", w.Code)
	}
}
