package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

func init() {
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
}

// --- file -------------------------------------------------------------------

func TestFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("  fix the parser bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := New(config.TaskSource{Kind: "file", Path: path}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if task.Description != "fix the parser bug" {
		t.Errorf("description = %q", task.Description)
	}
	// The second call must signal the end.
	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestFileSourceMissing(t *testing.T) {
	f, _ := New(config.TaskSource{Kind: "file", Path: "/does/not/exist/task.md"}, logx.Global())
	if _, err := f.Next(context.Background()); err == nil {
		t.Fatal("a read error was expected")
	}
}

func TestFileSourceEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	os.WriteFile(path, []byte("   \n"), 0o644)
	f, _ := New(config.TaskSource{Kind: "file", Path: path}, logx.Global())
	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("an empty file must end the source, got %v", err)
	}
}

// TestFileSourceWithTaskList: a file holding several tasks must be refused with a
// message saying what to do, instead of processing only the first one.
func TestFileSourceWithTaskList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "several.json")
	os.WriteFile(path, []byte(`[{"description":"one"},{"description":"two"}]`), 0o644)

	f, _ := New(config.TaskSource{Kind: "file", Path: path}, logx.Global())
	_, err := f.Next(context.Background())
	if err == nil {
		t.Fatal("a list of tasks must be refused")
	}
	if !strings.Contains(err.Error(), "queue") {
		t.Errorf("the error must suggest the alternative: %v", err)
	}
}

// --- queue ------------------------------------------------------------------

// TestQueueSourceOrderAndConsumption: files are taken in alphabetical order and
// marked as done, so work is not repeated after a restart.
func TestQueueSourceOrderAndConsumption(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"003-c.txt", "001-a.txt", "002-b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("task "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: time.Millisecond}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}

	var collected []string
	for i := 0; i < 3; i++ {
		task, err := f.Next(context.Background())
		if err != nil {
			t.Fatalf("error on task %d: %v", i, err)
		}
		collected = append(collected, task.Description)
	}
	expected := []string{"task 001-a.txt", "task 002-b.txt", "task 003-c.txt"}
	if strings.Join(collected, "|") != strings.Join(expected, "|") {
		t.Errorf("order = %v", collected)
	}

	// The originals must have been consumed.
	entries, _ := os.ReadDir(dir)
	done := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".done") {
			done++
		}
	}
	if done != 3 {
		t.Errorf("files marked as done = %d, expected 3", done)
	}
}

// TestQueueSourceCancels: an empty queue must not block shutdown.
func TestQueueSourceCancels(t *testing.T) {
	f, _ := New(config.TaskSource{Kind: "queue", Dir: t.TempDir(), Interval: 10 * time.Second}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.Next(ctx)
	if err != io.EOF {
		t.Errorf("with the context cancelled it must return io.EOF, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cancellation was not respected: %s", elapsed)
	}
}

func TestQueueSourceMissingDir(t *testing.T) {
	f, _ := New(config.TaskSource{Kind: "queue", Dir: "/does/not/exist/queue", Interval: time.Millisecond}, logx.Global())
	if _, err := f.Next(context.Background()); err == nil {
		t.Fatal("a missing directory was expected to be an error")
	}
}

// --- api --------------------------------------------------------------------

func TestAPISourceString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":"review the application logs"}`)
	}))
	defer srv.Close()

	f, err := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task"}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if task.Description != "review the application logs" {
		t.Errorf("description = %q", task.Description)
	}
}

func TestAPISourceObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":{"description":"generate the report","context":{"zone":"south"}}}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task"}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if task.Description != "generate the report" {
		t.Errorf("description = %q", task.Description)
	}
	if task.Context["zone"] != "south" {
		t.Errorf("context = %v", task.Context)
	}
}

func TestAPISourcePlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "task in plain text")
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if task.Description != "task in plain text" {
		t.Errorf("description = %q", task.Description)
	}
}

// TestAPISource204Ends: with no content the source finishes instead of waiting
// forever.
func TestAPISource204Ends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL}, logx.Global())
	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("204 must end the source, got %v", err)
	}
}

// TestAPISourceRetries: a temporary failure must not abort the run.
func TestAPISourceRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "down")
			return
		}
		fmt.Fprint(w, `{"task":"on the second try"}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Interval: time.Millisecond}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("it should recover: %v", err)
	}
	if task.Description != "on the second try" {
		t.Errorf("description = %q", task.Description)
	}
}

func TestAPISourceMissingField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"something_else":1}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task", Interval: time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("it must retry and end with the cancelled context, got %v", err)
	}
}

func TestAPISourceHeadersAndPost(t *testing.T) {
	var (
		method string
		header string
		body   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		header = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"task":"x"}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{
		Kind: "api", URL: srv.URL, Method: "POST", Field: "task",
		Body:    `{"queue":"general"}`,
		Headers: map[string]string{"Authorization": "Bearer token"},
	}, logx.Global())

	if _, err := f.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if method != "POST" {
		t.Errorf("method = %q", method)
	}
	if header != "Bearer token" {
		t.Errorf("header = %q", header)
	}
	if body["queue"] != "general" {
		t.Errorf("body = %v", body)
	}
}

// --- constructors -----------------------------------------------------------

func TestNewTextAndFile(t *testing.T) {
	f, err := NewText("do this", "test")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := f.Next(context.Background())
	if task.Description != "do this" || task.Origin != "test" {
		t.Errorf("task = %+v", task)
	}

	path := filepath.Join(t.TempDir(), "t.txt")
	os.WriteFile(path, []byte("from file"), 0o644)
	ff, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	task2, _ := ff.Next(context.Background())
	if task2.Description != "from file" {
		t.Errorf("task = %+v", task2)
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := New(config.TaskSource{Kind: "telepathy"}, logx.Global()); err == nil {
		t.Fatal("an error was expected")
	}
}

func TestStdinSourceIsDefault(t *testing.T) {
	f, err := New(config.TaskSource{}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Describe(), "stdin") {
		t.Errorf("description = %q", f.Describe())
	}
	f.Close()
}
