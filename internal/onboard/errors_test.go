package onboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- error paths -------------------------------------------------------------

// failingReader fails after N successful reads, to exercise the branches that
// cannot be reached with a well-behaved terminal.
type failingReader struct {
	remaining int
}

func (f *failingReader) Read(b []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, errors.New("the terminal went away")
	}
	f.remaining--
	b[0] = '\n'
	return 1, nil
}

// TestReadErrorIsReported: a broken terminal must be reported, not swallowed.
func TestReadErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	_, err := Run(context.Background(), &failingReader{}, &out, filepath.Join(dir, "c.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Fatal("a read error must surface")
	}
	if !strings.Contains(err.Error(), "terminal went away") {
		t.Errorf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "c.yaml")); !os.IsNotExist(statErr) {
		t.Error("nothing may be written when the terminal fails")
	}
}

// TestReadErrorDuringTheModelQuestion, the anchor question and the key question.
func TestReadErrorAtEachQuestion(t *testing.T) {
	cases := []struct {
		name      string
		remaining int // reads that succeed before the failure
		preset    Answers
	}{
		{"provider", 0, Answers{}},
		{"base_url", 1, Answers{}},
		{"key", 2, Answers{}},
		{"model", 3, Answers{}},
		{"anchor", 4, Answers{}},
		{"review", 5, Answers{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var out bytes.Buffer
			in := &failingReader{remaining: tc.remaining}
			_, err := Run(context.Background(), in, &out, filepath.Join(dir, "c.yaml"), tc.preset, fixedTime())
			if err == nil {
				t.Fatalf("the failure at %s must surface", tc.name)
			}
		})
	}
}

// TestCancellingAtEachQuestion: "q" at any point leaves nothing behind.
func TestCancellingAtEachQuestion(t *testing.T) {
	cases := []struct {
		name    string
		answers []string
	}{
		{"provider", []string{"q"}},
		{"base_url", []string{"openai", "q"}},
		{"key", []string{"openai", "", "q"}},
		{"model", []string{"openai", "", "", "quit"}},
		{"anchor", []string{"openai", "", "", "1", "q"}},
		{"anchor command", []string{"openai", "", "", "1", "2", "q"}},
		{"review", []string{"openai", "", "", "1", "3", "q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "c.yaml")
			in := strings.NewReader(strings.Join(tc.answers, "\n") + "\n")
			var out bytes.Buffer
			_, err := Run(context.Background(), in, &out, path, Answers{}, fixedTime())
			if err != ErrCancelled {
				t.Fatalf("err = %v, want ErrCancelled", err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Error("nothing may be written after cancelling")
			}
		})
	}
}

// TestModelQuestionGivesUpAfterThreeAttempts.
func TestModelQuestionGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "", "", "0", "0", "0"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// TestAnchorQuestionGivesUpAfterThreeAttempts.
func TestAnchorQuestionGivesUpAfterThreeAttempts(t *testing.T) {
	dir := t.TempDir()
	_, _, err := run(context.Background(), t, dir, []string{"openai", "", "", "1", "x", "y", "z"}, Answers{})
	if err == nil || !strings.Contains(err.Error(), "three attempts") {
		t.Errorf("err = %v", err)
	}
}

// TestConfigurationCannotBeWrittenIsReported: an impossible destination must be
// reported and must not leave a partial file.
func TestConfigurationCannotBeWrittenIsReported(t *testing.T) {
	dir := t.TempDir()
	old := writeFileAtomicFn
	writeFileAtomicFn = func(string, []byte) error { return errors.New("read-only file system") }
	t.Cleanup(func() { writeFileAtomicFn = old })
	_, _, err := run(context.Background(), t, dir, []string{"openai", "", "", "1", "3", ""}, Answers{})
	if err == nil {
		t.Error("an unwritable configuration path must be reported")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(statErr) {
		t.Error("nothing may be left behind")
	}
}

// TestCredentialsWriteFailureIsReported: if the configuration can be written but
// the key file cannot, the wizard must say so instead of pretending it worked.
func TestCredentialsWriteFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	old := writeFileAtomicFn
	writeFileAtomicFn = func(p string, c []byte) error {
		if strings.HasSuffix(p, ".env") {
			return errors.New("read-only file system")
		}
		return old(p, c)
	}
	t.Cleanup(func() { writeFileAtomicFn = old })
	_, _, err := run(context.Background(), t, dir, []string{"openai", "", "sk-abc", "1", "3", ""}, Answers{})
	if err == nil {
		t.Error("a credentials write failure must be reported")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "config.yaml")); statErr != nil {
		t.Error("the configuration itself was written, so it must be there")
	}
}

// TestRenderIncludesTheSeparatorBetweenArguments exercises the branch that writes
// the comma, which only appears with two arguments or more.
func TestRenderIncludesTheSeparatorBetweenArguments(t *testing.T) {
	out := string(renderConfig(configValues{
		provider: "openai", model: "m", baseURL: "u",
		anchorCommand: "go", anchorArgs: []string{"test", "./..."},
	}))
	if !strings.Contains(out, "args: [test, ./...]") {
		t.Errorf("arguments must be separated:\n%s", out)
	}
}

// --- the injectable filesystem seams -----------------------------------------

// TestWriteFileAtomicReportsEveryFilesystemFailure drives the branches that a real
// test cannot provoke: no temporary file, no chmod, no rename, no close.
func TestWriteFileAtomicReportsEveryFilesystemFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")

	t.Run("create", func(t *testing.T) { checkAtomicWriteCreateFailure(t, target) })

	t.Run("chmod", func(t *testing.T) {
		saved := chmodFile
		defer func() { chmodFile = saved }()
		chmodFile = func(string, os.FileMode) error { return errors.New("chmod refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed chmod must be reported")
		}
		if _, statErr := os.Stat(target); statErr == nil {
			t.Error("nothing may be left in place when it fails midway")
		}
	})

	t.Run("rename", func(t *testing.T) {
		saved := renameFile
		defer func() { renameFile = saved }()
		renameFile = func(string, string) error { return errors.New("rename refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed rename must be reported")
		}
	})

	t.Run("close", func(t *testing.T) {
		saved := closeFile
		defer func() { closeFile = saved }()
		closeFile = func(*os.File) error { return errors.New("close refused") }
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed close must be reported")
		}
	})

	t.Run("close and write", func(t *testing.T) {
		// A directory cannot be written to as a file: this exercises a write that
		// fails after the temporary file exists.
		saved := createTemp
		defer func() { createTemp = saved }()
		createTemp = func(_ string, _ string) (*os.File, error) {
			f, err := os.CreateTemp(t.TempDir(), "readonly-*")
			if err != nil {
				return nil, err
			}
			f.Close()
			return os.Open(f.Name()) // read-only handle: writing fails
		}
		if err := writeFileAtomic(target, []byte("x")); err == nil {
			t.Error("a failed write must be reported")
		}
	})
}

// checkAtomicWriteCreateFailure: a temporary file that cannot be created is reported with
// its cause.
func checkAtomicWriteCreateFailure(t *testing.T, target string) {
	saved := createTemp
	defer func() { createTemp = saved }()
	createTemp = func(string, string) (*os.File, error) {
		return nil, errors.New("no space left on device")
	}
	if err := writeFileAtomic(target, []byte("x")); err == nil {
		t.Error("a failed create must be reported")
	} else if !strings.Contains(err.Error(), "no space left") {
		t.Errorf("err = %v", err)
	} else {
		// the expected outcome: nothing more to check
	}
}
