// Package task obtains tasks from the configured source: stdin, a file, a
// directory queue or an HTTP API.
//
// The source is a small abstraction so the rest of the agent does not care where
// the work comes from, and so it can be replaced in tests.
package task

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

// Task is one unit of work.
type Task struct {
	Description string            `json:"description"`
	Context     map[string]string `json:"context,omitempty"`
	Origin      string            `json:"origin,omitempty"`
}

// Source delivers tasks until it returns io.EOF.
type Source interface {
	// Next returns the next task. It returns io.EOF when there are no more.
	Next(ctx context.Context) (Task, error)
	// Close releases resources (files, connections).
	Close() error
	// Describe describes the source for the log.
	Describe() string
}

// New builds the source according to the configuration.
func New(cfg config.TaskSource, log *logx.Logger) (Source, error) {
	if log == nil {
		log = logx.Global()
	}
	switch strings.ToLower(cfg.Kind) {
	case "", "stdin":
		return &stdinSource{reader: bufio.NewReader(os.Stdin)}, nil
	case "file":
		return &fileSource{path: cfg.Path}, nil
	case "queue":
		return &queueSource{dir: cfg.Dir, interval: cfg.Interval, log: log}, nil
	case "api":
		return newAPISource(cfg, log)
	default:
		return nil, fmt.Errorf("unknown task source: %q", cfg.Kind)
	}
}

// --- stdin ------------------------------------------------------------------

type stdinSource struct {
	reader *bufio.Reader

	// lines delivers the lines read by the reader goroutine. It exists because
	// ReadString BLOCKS: if it were called directly, the context would never be
	// evaluated and a SIGINT could not cancel the agent while it waits for input
	// (graceful shutdown would be useless). With the read on its own goroutine,
	// the loop can serve the context.
	lines chan lineResult
	once  sync.Once
}

type lineResult struct {
	text string
	err  error
}

func (f *stdinSource) Describe() string { return "stdin" }
func (f *stdinSource) Close() error     { return nil }

// Next waits for a line with contents, respecting cancellation.
//
// The read runs on its own goroutine because ReadString blocks: without that the
// context is never evaluated and a SIGINT cannot cancel the agent while it waits
// for input (graceful shutdown would be useless, and in fact it was: a test with
// an already-cancelled context hung forever).
func (f *stdinSource) Next(ctx context.Context) (Task, error) {
	f.once.Do(func() {
		f.lines = make(chan lineResult, 1)
		go f.readLines()
	})

	for {
		select {
		case <-ctx.Done():
			// Cancelled (SIGINT/SIGTERM): finish without an error, so graceful
			// shutdown is not confused with a source failure.
			return Task{}, io.EOF
		case result, open := <-f.lines:
			if !open {
				return Task{}, io.EOF
			}
			text := strings.TrimSpace(result.text)
			if text != "" {
				return Task{Description: text, Origin: "stdin"}, nil
			}
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return Task{}, io.EOF
				}
				return Task{}, fmt.Errorf("error reading from stdin: %w", result.err)
			}
		}
	}
}

// readLines feeds the channel until the input runs out.
func (f *stdinSource) readLines() {
	defer close(f.lines)
	for {
		line, err := f.reader.ReadString('\n')
		f.lines <- lineResult{text: line, err: err}
		if err != nil {
			return
		}
	}
}

// --- file -------------------------------------------------------------------

type fileSource struct {
	path string
	done bool
}

func (f *fileSource) Describe() string { return "file:" + f.path }
func (f *fileSource) Close() error     { return nil }

// Next reads the file once and returns a task with its contents.
func (f *fileSource) Next(ctx context.Context) (Task, error) {
	if f.done {
		return Task{}, io.EOF
	}
	f.done = true

	data, err := os.ReadFile(f.path)
	if err != nil {
		return Task{}, fmt.Errorf("could not read the task in %q: %w", f.path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return Task{}, io.EOF
	}

	// A file holding a JSON array of tasks is refused: silently running only the
	// first one would hide the rest of the work.
	var list []Task
	if json.Unmarshal(data, &list) == nil && len(list) > 0 {
		return Task{}, fmt.Errorf("the file %q holds %d tasks; use task_source.kind=queue or one task per file", f.path, len(list))
	}
	return Task{Description: text, Origin: "file:" + f.path}, nil
}

// --- directory queue --------------------------------------------------------

// queueSource takes files from a directory in alphabetical order and consumes
// them (moving them to .done) so work is not repeated after a restart.
type queueSource struct {
	dir      string
	interval time.Duration
	log      *logx.Logger
	pending  []string
}

func (f *queueSource) Describe() string { return "queue:" + f.dir }
func (f *queueSource) Close() error     { return nil }

func (f *queueSource) Next(ctx context.Context) (Task, error) {
	for {
		if len(f.pending) == 0 {
			list, err := f.list()
			if err != nil {
				return Task{}, err
			}
			f.pending = list
		}

		if len(f.pending) > 0 {
			path := f.pending[0]
			f.pending = f.pending[1:]

			data, err := os.ReadFile(path)
			if err != nil {
				f.log.Warn("could not read the queue file", "path", path, "error", err)
				continue
			}
			t := Task{Description: strings.TrimSpace(string(data)), Origin: path}
			if err := os.Rename(path, path+".done"); err != nil {
				f.log.Warn("could not mark the file as done", "path", path, "error", err)
			}
			return t, nil
		}

		// No work right now: wait for the polling interval.
		wait := f.interval
		if wait <= 0 {
			wait = 30 * time.Second
		}
		f.log.Debug("empty queue, waiting", "dir", f.dir, "wait", wait.String())
		select {
		case <-ctx.Done():
			return Task{}, io.EOF
		case <-time.After(wait):
		}
	}
}

func (f *queueSource) list() ([]string, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, fmt.Errorf("could not read the queue directory %q: %w", f.dir, err)
	}
	var pending []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".done") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pending = append(pending, filepath.Join(f.dir, e.Name()))
	}
	sort.Strings(pending)
	return pending, nil
}

// --- API --------------------------------------------------------------------

type apiSource struct {
	cfg  config.TaskSource
	http *http.Client
	log  *logx.Logger
}

func newAPISource(cfg config.TaskSource, log *logx.Logger) (*apiSource, error) {
	if cfg.URL == "" {
		return nil, errors.New("task_source.kind=api requires url")
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	return &apiSource{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
		log:  log,
	}, nil
}

func (f *apiSource) Describe() string { return "api:" + f.cfg.URL }
func (f *apiSource) Close() error     { return nil }

func (f *apiSource) Next(ctx context.Context) (Task, error) {
	for {
		t, err := f.poll(ctx)
		if err == nil {
			return t, nil
		}
		if errors.Is(err, io.EOF) {
			return Task{}, io.EOF
		}
		f.log.Warn("API poll failed", "url", f.cfg.URL, "error", err)

		wait := f.cfg.Interval
		if wait <= 0 {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return Task{}, io.EOF
		case <-time.After(wait):
		}
	}
}

func (f *apiSource) poll(ctx context.Context) (Task, error) {
	var body io.Reader
	if f.cfg.Body != "" && strings.ToUpper(f.cfg.Method) == http.MethodPost {
		body = bytes.NewReader([]byte(f.cfg.Body))
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(f.cfg.Method), f.cfg.URL, body)
	if err != nil {
		return Task{}, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := f.http.Do(req)
	if err != nil {
		return Task{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Task{}, err
	}
	if resp.StatusCode == http.StatusNoContent {
		return Task{}, io.EOF
	}
	if resp.StatusCode != http.StatusOK {
		return Task{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	field := f.cfg.Field
	if field == "" {
		field = "task"
	}

	// Three shapes are accepted: {"task": "..."}, {"task": {...}} or plain text.
	var generic map[string]any
	if json.Unmarshal(data, &generic) == nil {
		value, ok := generic[field]
		if !ok {
			return Task{}, fmt.Errorf("the response does not carry the field %q", field)
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return Task{}, errors.New("the task field is empty")
			}
			return Task{Description: v, Origin: f.cfg.URL}, nil
		case map[string]any:
			// {"task": {"description": "...", "context": {...}}}
			var t Task
			raw, _ := json.Marshal(v)
			if err := json.Unmarshal(raw, &t); err == nil {
				if t.Description == "" {
					t.Description = strings.TrimSpace(string(raw))
				}
				t.Origin = f.cfg.URL
				return t, nil
			}
		}
		return Task{}, fmt.Errorf("the field %q has an unsupported type", field)
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return Task{}, errors.New("the API returned an empty body")
	}
	return Task{Description: text, Origin: f.cfg.URL}, nil
}

// --- Helper constructors (used by the command line) -------------------------

// textSource delivers a single, already-known task and then finishes.
type textSource struct {
	task Task
	done bool
}

func (f *textSource) Describe() string { return "text:" + f.task.Origin }
func (f *textSource) Close() error     { return nil }

func (f *textSource) Next(ctx context.Context) (Task, error) {
	if f.done {
		return Task{}, io.EOF
	}
	f.done = true
	return f.task, nil
}

// NewText builds a single-task source (the -task flag).
func NewText(text, origin string) (Source, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("the task is empty")
	}
	return &textSource{task: Task{Description: strings.TrimSpace(text), Origin: origin}}, nil
}

// NewFile builds a source from a file (the -task-file flag), bypassing the
// configuration.
func NewFile(path string) (Source, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("the task file path is empty")
	}
	return &fileSource{path: path}, nil
}
