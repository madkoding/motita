package onboard

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKeys is a terminal that always switches, and counts how often it was put back.
type fakeKeys struct {
	switches, restores int
	// failAfter makes every switch after this many fail, the way a terminal that went away does.
	failAfter int
}

func (k *fakeKeys) mode() (func(), bool) {
	k.switches++
	if k.failAfter > 0 && k.switches > k.failAfter {
		return func() {}, false
	}
	return func() { k.restores++ }, true
}

// runKeys runs the setup with the keyboard driving the menus, typing the given keys.
func runKeys(ctx context.Context, t *testing.T, keys string, k *fakeKeys) (string, Result, error) {
	t.Helper()
	var out bytes.Buffer
	res, err := RunWithKeys(ctx, strings.NewReader(keys), &out, filepath.Join(t.TempDir(), "config.yaml"), Answers{}, fixedTime(), k.mode)
	return out.String(), res, err
}

const (
	down = "\x1b[B"
	up   = "\x1b[A"
)

// The arrows choose, Enter takes the highlighted option, and anything typed is still an answer.
func TestTheArrowsDriveTheMenus(t *testing.T) {
	k := &fakeKeys{}
	keys := down + down + down + down + "\r" + // provider: Anthropic, the fifth
		"sk-ant-0123456789\r" + // the key, masked as it is typed
		up + "\r" + // model: up from the first wraps to the last
		"3\r" + // check: typed, as before
		"\r" // review: save
	out, res, err := runKeys(context.Background(), t, keys, k)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, stripANSI(out))
	}
	anthropic, _ := Lookup("anthropic")
	if res.Provider.ID != "anthropic" || res.Model != anthropic.Models[len(anthropic.Models)-1].ID || res.CredentialsPath == "" {
		t.Errorf("res = %+v", res)
	}
	plain := stripANSI(out)
	if strings.Contains(plain, "sk-ant-0123456789") {
		t.Error("the key must be masked as it is typed")
	}
	for _, want := range []string{"Use ↑↓ to choose", "•••••", "›  5  Anthropic Claude", "Provider [5]: 5"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, plain)
		}
	}
	if k.restores == 0 || k.restores != k.switches {
		t.Errorf("every switch must be put back: %d switches, %d restores", k.switches, k.restores)
	}
}

// The editing keys: backspace, keys that are not text, Escape and the SS3 arrows.
func TestTheLineEditor(t *testing.T) {
	k := &fakeKeys{}
	keys := "\x1bOB" + "\x1b[C" + "\x1b" + "\x01" + "\r" + // provider: SS3 down to Codex; right, Escape and Ctrl+A do nothing
		"\r" + // sign-in: the first option is the stub's login
		"xy\x7f\x7fmodel-日本\r" + // model: typed, with a correction and characters past ASCII
		"\r" + "\r"
	stubDirectAuth(t, "")
	_, res, err := runKeys(context.Background(), t, keys, k)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Provider.ID != "codex" || res.Model != "model-日本" || !res.LoggedIn {
		t.Errorf("res = %+v", res)
	}
}

// q, Ctrl+C, Ctrl+D on an empty line and the end of the input all leave, and write nothing.
func TestLeavingFromTheKeyboard(t *testing.T) {
	for name, keys := range map[string]string{
		"q":      "q\r",
		"ctrl+c": "\x03",
		"ctrl+d": "\x04",
		"eof":    down,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := runKeys(context.Background(), t, keys, &fakeKeys{}); !errors.Is(err, ErrCancelled) {
				t.Errorf("err = %v, want ErrCancelled", err)
			}
		})
	}
}

func TestACancelledContextLeavesTheEditor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	var out bytes.Buffer
	if _, err := RunWithKeys(ctx, pr, &out, filepath.Join(t.TempDir(), "c.yaml"), Answers{}, fixedTime(), (&fakeKeys{}).mode); !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

// A terminal that cannot be switched leaves the setup reading lines, exactly as before.
func TestWithoutCharacterModeTheSetupReadsLines(t *testing.T) {
	never := func() (func(), bool) { return func() {}, false }
	var out bytes.Buffer
	res, err := RunWithKeys(context.Background(), strings.NewReader("anthropic\n\n\n3\n\n"), &out, filepath.Join(t.TempDir(), "c.yaml"), Answers{}, fixedTime(), never)
	if err != nil || res.Provider.ID != "anthropic" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(stripANSI(out.String()), "Use ↑↓") {
		t.Error("the arrows must not be offered when they cannot work")
	}
}

// A terminal that stops switching halfway falls back to lines for the questions that follow.
func TestATerminalThatStopsSwitchingFallsBackToLines(t *testing.T) {
	k := &fakeKeys{failAfter: 1} // the probe works, every question after it does not
	_, res, err := runKeys(context.Background(), t, "anthropic\n\n\n3\n\n", k)
	if err != nil || res.Provider.ID != "anthropic" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// A character cut short is kept as the replacement character rather than swallowing what follows.
func TestABrokenCharacterIsReplaced(t *testing.T) {
	s := &session{in: bufio.NewReader(strings.NewReader(""))}
	if r := s.readRune(0xe6); r != '\uFFFD' {
		t.Errorf("readRune = %q, want the replacement character", r)
	}
}

// An escape that is not a CSI or SS3 sequence ends at the byte after it.
func TestReadEscapeShapes(t *testing.T) {
	for in, want := range map[string]string{
		"":     "\x1b",
		"x":    "\x1b",
		"[1;5": "\x1b[1;5",
		"OA":   "\x1bOA",
	} {
		s := &session{in: bufio.NewReader(strings.NewReader(in))}
		s.in.Peek(1) // fill the buffer, as a terminal's single write does
		if got := s.readEscape(); got != want {
			t.Errorf("readEscape(%q) = %q, want %q", in, got, want)
		}
		if in == "x" {
			if next, _ := s.in.ReadByte(); next != 'x' {
				t.Errorf("the key after a bare Escape must be kept, got %q", next)
			}
		}
	}
}

func TestWithDefault(t *testing.T) {
	if got := withDefault("Check [1]:", nil); got != "Check [1]:" {
		t.Errorf("no menu: %q", got)
	}
	if got := withDefault("Model [1, or type any model id]:", &menu{sel: 2}); got != "Model [3, or type any model id]:" {
		t.Errorf("withDefault = %q", got)
	}
}
