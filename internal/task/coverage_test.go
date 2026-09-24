package task

import (
	"bufio"
	"context"
	"errors"
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

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// contains reports whether sub appears in s (kept dependency-free on purpose).
func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- interface: Describe and Close of every source --------------------------

// TestDescribeAndCloseOfEverySource: they are part of the interface and show up
// in the agent's startup log, so all of them must provide them and never fail.
func TestDescribeAndCloseOfEverySource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t.md"), []byte("x"), 0o644)

	sources := []struct {
		cfg      config.TaskSource
		contains string
	}{
		{config.TaskSource{Kind: "stdin"}, "stdin"},
		{config.TaskSource{Kind: "file", Path: filepath.Join(dir, "t.md")}, "file:"},
		{config.TaskSource{Kind: "queue", Dir: dir, Interval: time.Millisecond}, "queue:"},
		{config.TaskSource{Kind: "api", URL: "http://example/tasks"}, "api:"},
	}
	for _, tc := range sources {
		f, err := New(tc.cfg, logx.Global())
		if err != nil {
			t.Fatalf("%s: %v", tc.cfg.Kind, err)
		}
		if !contains(f.Describe(), tc.contains) {
			t.Errorf("%s: description = %q", tc.cfg.Kind, f.Describe())
		}
		if err := f.Close(); err != nil {
			t.Errorf("%s: Close returned an error: %v", tc.cfg.Kind, err)
		}
	}
}

// --- stdin ------------------------------------------------------------------

// TestStdinSourceSkipsBlankLines: blank lines are not tasks.
func TestStdinSource(t *testing.T) {
	orig := os.Stdin
	defer func() { os.Stdin = orig }()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	fmt.Fprintln(write, "")
	fmt.Fprintln(write, "  ")
	fmt.Fprintln(write, "first task")
	fmt.Fprintln(write, "second task")
	write.Close()
	os.Stdin = read

	f, err := New(config.TaskSource{Kind: "stdin"}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}

	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if task.Description != "first task" || task.Origin != "stdin" {
		t.Errorf("task = %+v", task)
	}

	task, err = f.Next(context.Background())
	if err != nil || task.Description != "second task" {
		t.Errorf("second task = %+v, %v", task, err)
	}

	// When the input runs out, io.EOF.
	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

// TestStdinSourceCancelledContext: with no data and a cancelled context it must
// return io.EOF instead of blocking.
func TestStdinSourceCancelledContext(t *testing.T) {
	orig := os.Stdin
	defer func() { os.Stdin = orig }()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	fmt.Fprintln(write, "") // a blank line: it is skipped and the context is evaluated
	os.Stdin = read

	f, err := New(config.TaskSource{Kind: "stdin"}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	_, err = f.Next(ctx)
	if err != io.EOF && err != context.DeadlineExceeded {
		t.Errorf("with the context cancelled it must finish: %v", err)
	}
}

// TestStdinSourceReadError: a read failure (closed pipe) must be reported instead
// of hanging the run.
func TestStdinSourceReadError(t *testing.T) {
	orig := os.Stdin
	defer func() { os.Stdin = orig }()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	write.Close() // writing end closed: ReadString fails at once
	os.Stdin = read

	f, _ := New(config.TaskSource{Kind: "stdin"}, logx.Global())
	_, err = f.Next(context.Background())
	if err == nil {
		t.Error("a read failure must be reported")
	}
	read.Close()
}

// --- file -------------------------------------------------------------------

// TestFileSourceWithJSONList: a file holding several tasks must be refused with a
// message saying what to do, instead of processing only the first.
func TestFileSourceWithJSONList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "several.json")
	os.WriteFile(path, []byte(`[{"description":"one"},{"description":"two"}]`), 0o644)

	f, _ := New(config.TaskSource{Kind: "file", Path: path}, logx.Global())
	_, err := f.Next(context.Background())
	if err == nil {
		t.Fatal("a list of tasks must be refused")
	}
	if !contains(err.Error(), "queue") {
		t.Errorf("the error must suggest the alternative: %v", err)
	}
}

// --- queue ------------------------------------------------------------------

// TestQueueSourceWaitsAndStaysAlive: with an empty queue it waits for the
// interval and then takes whatever shows up.
func TestQueueSourceWaitsAndStaysAlive(t *testing.T) {
	dir := t.TempDir()
	f, err := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: 50 * time.Millisecond}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}

	// The file is created after the source has already looked once.
	go func() {
		time.Sleep(120 * time.Millisecond)
		os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new task"), 0o644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	task, err := f.Next(ctx)
	if err != nil {
		t.Fatalf("it should pick up the file that appears: %v", err)
	}
	if task.Description != "new task" {
		t.Errorf("description = %q", task.Description)
	}
}

// TestQueueSourceIgnoresHiddenAndDoneFiles.
func TestQueueSourceIgnoresHiddenAndDoneFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("no"), 0o644)
	os.WriteFile(filepath.Join(dir, "already.done"), []byte("no"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdirectory"), 0o755)
	os.WriteFile(filepath.Join(dir, "good.txt"), []byte("yes"), 0o644)

	f, _ := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: time.Millisecond}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != "yes" {
		t.Errorf("it took a file it should have ignored: %q", task.Description)
	}
}

// TestQueueSourceUnreadableFile: a file that disappears between the listing and
// the read must not bring the source down.
func TestQueueSourceUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first"), 0o644)

	f, _ := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: 10 * time.Millisecond}, logx.Global())
	// The first one is consumed and the directory is deleted so the next listing
	// fails; the source must record it and carry on (or return an error).
	if _, err := f.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := f.Next(ctx)
	if err == nil {
		t.Error("with no directory it must return a read error")
	}
}

// TestQueueSourceUnreadableFileInList: a file listed and then deleted before it is
// read must be skipped, not fatal (the classic race in a watched directory).
func TestQueueSourceUnreadableFileInList(t *testing.T) {
	dir := t.TempDir()
	// Two files: the first listing sees "a.txt" and "b.txt"; a.txt is deleted
	// right after listing, so reading it fails and the source must move on to b.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("gone"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("still here"), 0o644)

	f, _ := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: time.Millisecond}, logx.Global())
	src := f.(*queueSource)
	// Force the pending list, then delete the first entry before it is read.
	src.pending = []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")}
	os.Remove(filepath.Join(dir, "a.txt"))

	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("it must skip the unreadable file: %v", err)
	}
	if task.Description != "still here" {
		t.Errorf("description = %q", task.Description)
	}
}

// TestQueueSourceRenameFails: if the file cannot be marked as done the task is
// still delivered (repeating work is better than losing it).
func TestQueueSourceRenameFails(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t.txt"), []byte("work"), 0o644)

	f, _ := New(config.TaskSource{Kind: "queue", Dir: dir, Interval: time.Millisecond}, logx.Global())
	src := f.(*queueSource)
	// A directory with the target name makes the rename fail.
	os.MkdirAll(filepath.Join(dir, "t.txt.done"), 0o755)
	src.pending = []string{filepath.Join(dir, "t.txt")}

	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatalf("the task must still be delivered: %v", err)
	}
	if task.Description != "work" {
		t.Errorf("description = %q", task.Description)
	}
}

// --- API --------------------------------------------------------------------

// TestAPISourceFailuresRetried: failures are retried until the context runs out.
func TestAPISourceFailuresRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := f.Next(ctx)
	if err != io.EOF {
		t.Errorf("with the context expired it must return io.EOF: %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("it should retry: only %d call(s)", calls)
	}
}

// TestAPISourceNetworkError: an unreachable URL does not abort, it retries.
func TestAPISourceNetworkError(t *testing.T) {
	f, err := New(config.TaskSource{Kind: "api", URL: "http://127.0.0.1:1/tasks", Interval: 10 * time.Millisecond}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// TestAPISourceUnsupportedList: a field that is a list is refused clearly.
func TestAPISourceUnsupportedList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":["one","two"]}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task", Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// TestAPISourceEmptyField: a field that is present but empty counts as an error.
func TestAPISourceEmptyField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":"   "}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task", Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// TestAPISourceEmptyBody: a 200 response with no body is an error.
func TestAPISourceEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// TestAPISourceNestedObjectWithDescription: the task object is used as it comes.
func TestAPISourceNestedObjectWithDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":{"description":"do this","context":{"a":"b"}}}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task"}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != "do this" || task.Context["a"] != "b" {
		t.Errorf("task = %+v", task)
	}
}

// TestAPISourceNestedObjectWithoutDescription: an object with no description keeps
// its raw JSON, so no information is lost.
func TestAPISourceNestedObjectWithoutDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":{"context":{"a":"b"}}}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task"}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(task.Description, "a") {
		t.Errorf("the raw content must be preserved: %q", task.Description)
	}
}

// TestAPISourceDefaultMethod: with no explicit method, GET is used.
func TestAPISourceDefaultMethod(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		fmt.Fprint(w, `{"task":"x"}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL}, logx.Global())
	if _, err := f.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet {
		t.Errorf("method = %q", method)
	}
}

// TestAPISourceRequiresURL: without a URL the source cannot be built.
func TestAPISourceRequiresURL(t *testing.T) {
	if _, err := New(config.TaskSource{Kind: "api"}, logx.Global()); err == nil {
		t.Error("api without url must be an error")
	}
}

// TestAPISourceInvalidRequest: an unparsable URL is reported as an invalid
// request, not a panic.
func TestAPISourceInvalidRequest(t *testing.T) {
	f, err := New(config.TaskSource{Kind: "api", URL: "://bad url", Interval: 10 * time.Millisecond}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// --- constructors -----------------------------------------------------------

func TestNewTextEmpty(t *testing.T) {
	if _, err := NewText("", "x"); err == nil {
		t.Error("an empty task must be an error")
	}
}

func TestNewFileEmpty(t *testing.T) {
	if _, err := NewFile(""); err == nil {
		t.Error("an empty path must be an error")
	}
}

func TestDescribeOfHelperSources(t *testing.T) {
	f, err := NewText("t", "command-line")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(f.Describe(), "command-line") {
		t.Errorf("description = %q", f.Describe())
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "t.md")
	os.WriteFile(path, []byte("x"), 0o644)
	ff, err := NewFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(ff.Describe(), "file:") {
		t.Errorf("description = %q", ff.Describe())
	}
	if err := ff.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

// TestAPISourceValidJSONWithoutField: valid JSON that does not carry the requested
// field.
func TestAPISourceJSONWithoutField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"another":"thing"}`)
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Field: "task", Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := f.Next(ctx)
	if err != io.EOF {
		t.Errorf("error = %v", err)
	}
}

// --- Remaining branches -----------------------------------------------------

// TestNewWithNilLogger: constructing a source with no logger must fall back to
// the global one instead of panicking.
func TestNewWithNilLogger(t *testing.T) {
	f, err := New(config.TaskSource{Kind: "stdin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("the source must not be nil")
	}
	f.Close()
}

// TestQueueSourceDefaultInterval: with no configured interval the default (30 s)
// applies, and cancellation must still be immediate.
func TestQueueSourceDefaultInterval(t *testing.T) {
	f, err := New(config.TaskSource{Kind: "queue", Dir: t.TempDir()}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Fatalf("with the context cancelled it must return io.EOF, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the default interval must not delay the shutdown: %s", elapsed)
	}
}

// TestAPISourceDefaultInterval: the same for the API source.
func TestAPISourceDefaultInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, err := New(config.TaskSource{Kind: "api", URL: srv.URL}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Fatalf("with the context cancelled it must return io.EOF, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the default interval must not delay the shutdown: %s", elapsed)
	}
}

// TestAPISourceUnreadableBody: a body that cannot be read must be reported as a
// failure (and retried) instead of being taken as an empty task.
func TestAPISourceUnreadableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A length longer than what is sent makes the read fail.
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short body")
	}))
	defer srv.Close()

	f, _ := New(config.TaskSource{Kind: "api", URL: srv.URL, Interval: 10 * time.Millisecond}, logx.Global())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.Next(ctx); err != io.EOF {
		t.Errorf("a truncated body must be retried and end with the cancelled context, got %v", err)
	}
}

// TestTextSourceEndsAfterOneTask: the single-task source delivers once and then
// reports the end, which is what stops the agent's loop.
func TestTextSourceEndsAfterOneTask(t *testing.T) {
	f, err := NewText("only task", "unit")
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != "only task" {
		t.Errorf("description = %q", task.Description)
	}
	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("the second call must return io.EOF, got %v", err)
	}
}

// TestStdinSourceSkipsBlankLineThenReadsTheNext: a blank line is skipped and the
// following line is delivered, without losing it.
func TestStdinSourceSkipsBlankLineThenReadsTheNext(t *testing.T) {
	orig := os.Stdin
	defer func() { os.Stdin = orig }()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	fmt.Fprintln(write, "")
	fmt.Fprintln(write, "the real task")
	write.Close()
	os.Stdin = read

	f, _ := New(config.TaskSource{Kind: "stdin"}, logx.Global())
	task, err := f.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != "the real task" {
		t.Errorf("description = %q", task.Description)
	}
}

// TestStdinSourceClosedChannelEnds: if the reader goroutine closes the channel
// (input exhausted), Next must report the end instead of blocking.
func TestStdinSourceClosedChannelEnds(t *testing.T) {
	f := &stdinSource{reader: bufio.NewReader(strings.NewReader(""))}
	f.once.Do(func() {
		f.lines = make(chan lineResult, 1)
		close(f.lines) // closed with no pending value
	})

	if _, err := f.Next(context.Background()); err != io.EOF {
		t.Errorf("a closed channel must end the source, got %v", err)
	}
}

// TestStdinSourceReportsReadError: a read failure that is not io.EOF must be
// reported as an error (with the cause), not silently treated as the end.
func TestStdinSourceReportsReadError(t *testing.T) {
	f := &stdinSource{reader: bufio.NewReader(strings.NewReader(""))}
	boom := errors.New("device on fire")
	f.once.Do(func() {
		f.lines = make(chan lineResult, 1)
		// A blank line: it is skipped, so the error is inspected.
		f.lines <- lineResult{text: "   ", err: boom}
		close(f.lines)
	})

	_, err := f.Next(context.Background())
	if err == nil {
		t.Fatal("the read error must be reported")
	}
	if !strings.Contains(err.Error(), "device on fire") {
		t.Errorf("the cause must be preserved: %v", err)
	}
}
