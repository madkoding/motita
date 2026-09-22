// Package logx implements structured JSON logging with size-based rotation,
// with no external dependencies.
//
// Every line is a standalone JSON object (JSON Lines), meant to be consumed by
// `jq`, a log collector or the anchor itself when checking invariants.
package logx

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level of severity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (n Level) String() string {
	switch n {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLevel turns text ("info") into a level; an error if it is not recognized.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return Debug, nil
	case "info", "":
		return Info, nil
	case "warn", "warning":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("unknown log level %q (use debug, info, warn or error)", s)
	}
}

// Options for building a Logger.
type Options struct {
	Path    string // destination file; empty = console only
	Level   Level
	Console bool
	MaxMB   int // maximum size before rotating (0 = no rotation)
	Backups int // how many rotated files to keep
}

// Logger writes JSON records safely for concurrent use.
type Logger struct {
	mu      sync.Mutex
	level   Level
	file    *os.File
	path    string
	written int64
	maxByte int64
	backups int
	console bool
	out     io.Writer
}

var (
	// global is the default log, handy when none is injected.
	global   = &Logger{level: Info, console: true, out: os.Stderr}
	globalMu sync.RWMutex
)

// New creates a logger. If opening the file fails it returns the error instead
// of falling back to a silent log.
func New(op Options) (*Logger, error) {
	l := &Logger{
		level:   op.Level,
		console: op.Console,
		out:     os.Stderr,
		backups: op.Backups,
	}
	if op.MaxMB > 0 {
		l.maxByte = int64(op.MaxMB) * 1024 * 1024
	}

	if op.Path != "" {
		if err := os.MkdirAll(filepath.Dir(op.Path), 0o755); err != nil {
			return nil, fmt.Errorf("could not create the log directory %q: %w", filepath.Dir(op.Path), err)
		}
		f, err := os.OpenFile(op.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("could not open the log %q: %w", op.Path, err)
		}
		l.file = f
		l.path = op.Path
		if info, err := f.Stat(); err == nil {
			l.written = info.Size()
		}
	}
	return l, nil
}

// Install replaces the global log.
func Install(l *Logger) {
	globalMu.Lock()
	global = l
	globalMu.Unlock()
}

// Global returns the global log.
func Global() *Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Close closes the log file if there is one.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Debug, Info, Warn and Error log at the corresponding level.
func (l *Logger) Debug(msg string, fields ...any) { l.log(Debug, msg, fields...) }
func (l *Logger) Info(msg string, fields ...any)  { l.log(Info, msg, fields...) }
func (l *Logger) Warn(msg string, fields ...any)  { l.log(Warn, msg, fields...) }
func (l *Logger) Error(msg string, fields ...any) { l.log(Error, msg, fields...) }

// log builds the JSON object and writes it to the file and/or the console.
func (l *Logger) log(level Level, msg string, fields ...any) {
	if level < l.level {
		return
	}

	event := make(map[string]any, 3+len(fields)/2)
	event["ts"] = time.Now().Format(time.RFC3339Nano)
	event["level"] = level.String()
	event["msg"] = msg
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		event[key] = jsonValue(fields[i+1])
	}

	line, err := json.Marshal(event)
	if err != nil {
		// The message is never lost because of an odd field: it degrades to
		// plain text.
		line, _ = json.Marshal(map[string]any{
			"ts":    event["ts"],
			"level": level.String(),
			"msg":   msg,
			"error": "could not serialize a field: " + err.Error(),
		})
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		n, err := l.file.Write(line)
		if err == nil {
			l.written += int64(n)
			l.rotateIfNeeded()
		}
	}
	if l.console && l.out != nil {
		l.out.Write(line)
	}
}

// rotateIfNeeded moves the current file to .1 and shifts the backups.
func (l *Logger) rotateIfNeeded() {
	if l.maxByte <= 0 || l.written < l.maxByte || l.file == nil {
		return
	}
	l.file.Close()
	l.file = nil

	if l.backups > 0 {
		// Shift .N-1 -> .N from highest to lowest so nothing is overwritten.
		for i := l.backups - 1; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", l.path, i)
			fresh := fmt.Sprintf("%s.%d", l.path, i+1)
			os.Rename(old, fresh)
		}
		os.Rename(l.path, l.path+".1")
	} else {
		os.Remove(l.path)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o644)
	if err != nil {
		// Without a file we carry on with the console: the agent must not die
		// because of the log.
		l.file = nil
		return
	}
	l.file = f
	l.written = 0
}

// RotatedFiles lists the existing backups (for diagnostics and tests).
func (l *Logger) RotatedFiles() []string {
	if l.path == "" {
		return nil
	}
	var out []string
	for i := 1; i <= l.backups; i++ {
		path := fmt.Sprintf("%s.%d", l.path, i)
		if _, err := os.Stat(path); err == nil {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// jsonValue converts types that are not directly serializable (errors).
func jsonValue(v any) any {
	switch t := v.(type) {
	case error:
		return t.Error()
	case time.Duration:
		return t.String()
	default:
		return v
	}
}
