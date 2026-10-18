package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/desktop"
	"github.com/madkoding/motita/internal/llm"
)

// shotImage is a picture the last round took.
var shotImage = llm.Image{MediaType: "image/jpeg", Data: []byte("j")}

// fakeDesk records the steps the actions ask of the desktop.
type fakeDesk struct {
	reqs []desktop.Request
	err  error
}

func (f *fakeDesk) Do(_ context.Context, r desktop.Request) (desktop.Result, error) {
	f.reqs = append(f.reqs, r)
	if f.err != nil {
		return desktop.Result{}, f.err
	}
	if r.Kind == desktop.Shot {
		return desktop.Result{Text: "screen 10x10", Picture: &desktop.Picture{JPEG: []byte("jpg"), Width: 10}}, nil
	}
	return desktop.Result{Text: "done " + r.Kind}, nil
}

func (f *fakeDesk) Label() string { return "fake" }

// deskAgent is an agent with desktop control on, the fake desktop, and an approver that answers
// answer.
func deskAgent(t *testing.T, fd *fakeDesk, answer bool) (*fixture, *[]ApprovalRequest) {
	t.Helper()
	e := fileAgent(t)
	old := newDesktop
	t.Cleanup(func() { newDesktop = old })
	newDesktop = func() (desktops, error) { return fd, nil }
	e.agent.cfg.Agent.DesktopControl = true
	asked := &[]ApprovalRequest{}
	e.agent.SetApprover(func(_ context.Context, r ApprovalRequest) (bool, error) {
		*asked = append(*asked, r)
		return answer, nil
	})
	return e, asked
}

func TestADesktopStepIsAskedThenDone(t *testing.T) {
	fd := &fakeDesk{}
	e, asked := deskAgent(t, fd, true)
	out, err := box(e, "desktop_click", "x: 10\ny: 20\nbutton: right\ndouble: true")
	if err != nil || !strings.Contains(out, "[done click]") {
		t.Fatalf("%v %q", err, out)
	}
	if len(*asked) != 1 || (*asked)[0].Rule != DesktopRule ||
		(*asked)[0].Command != "desktop_click x: 10 y: 20 button: right double: true" {
		t.Fatalf("asked %+v", *asked)
	}
	if r := fd.reqs[0]; r.X != 10 || r.Y != 20 || r.Button != "right" || !r.Double {
		t.Fatalf("%+v", r)
	}
	if e.agent.takeShot() != nil {
		t.Fatal("a click holds no picture")
	}
}

func TestADesktopPictureGoesToTheNextRoundOnceAndToThePerson(t *testing.T) {
	e, _ := deskAgent(t, &fakeDesk{}, true)
	out, err := box(e, "desktop_shot", "")
	if err != nil || !strings.Contains(out, "attached to the next message") || !strings.Contains(out, "Saved as") {
		t.Fatalf("%v %q", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, PreviewDir, "desktop-*.jpg"))
	if len(files) != 1 {
		t.Fatalf("previews: %v", files)
	}
	shots := e.agent.takeShot()
	if len(shots) != 1 || shots[0].MediaType != "image/jpeg" || string(shots[0].Data) != "jpg" {
		t.Fatalf("%+v", shots)
	}
	if e.agent.takeShot() != nil {
		t.Fatal("the picture is shown once")
	}
}

func TestADesktopPictureThatCannotBeSavedStillReachesTheModel(t *testing.T) {
	e, _ := deskAgent(t, &fakeDesk{}, true)
	// The preview folder is a file: nothing can be written under it.
	_ = os.MkdirAll(filepath.Join(e.dir, ".motita"), 0o755)
	_ = os.WriteFile(filepath.Join(e.dir, PreviewDir), []byte("x"), 0o644)
	out, _ := box(e, "desktop_shot", "")
	if strings.Contains(out, "Saved as") || len(e.agent.takeShot()) != 1 {
		t.Fatalf("%q", out)
	}
}

func TestDesktopStepsAreRefusedWhereTheyDoNotBelong(t *testing.T) {
	cases := map[string]func(a *Agent){
		"off":       func(a *Agent) { a.cfg.Agent.DesktopControl = false },
		"child":     func(a *Agent) { a.isChild = true },
		"read-only": func(a *Agent) { a.cfg.Agent.ReadOnly = true },
	}
	for name, set := range cases {
		e, asked := deskAgent(t, &fakeDesk{}, true)
		set(e.agent)
		out, err := box(e, "desktop_shot", "")
		if err == nil || !strings.Contains(out, "[refused:") || len(*asked) != 0 {
			t.Errorf("%s: %v %q %d", name, err, out, len(*asked))
		}
	}
}

func TestDesktopStepFailures(t *testing.T) {
	// A step the model wrote wrong is not asked about.
	e, asked := deskAgent(t, &fakeDesk{}, true)
	if out, _ := box(e, "desktop_move", "x: left"); !strings.Contains(out, "[failed:") || len(*asked) != 0 {
		t.Errorf("bad point: %q", out)
	}
	// Declined.
	e, _ = deskAgent(t, &fakeDesk{}, false)
	if out, err := box(e, "desktop_key", "ctrl+c"); err == nil || !strings.Contains(out, "declined") {
		t.Errorf("declined: %v %q", err, out)
	}
	// The desktop fails.
	e, _ = deskAgent(t, &fakeDesk{err: errors.New("xdotool failed")}, true)
	if out, _ := box(e, "desktop_type", "hola"); !strings.Contains(out, "[failed: xdotool failed]") {
		t.Errorf("step: %q", out)
	}
	// There is no desktop.
	e, _ = deskAgent(t, &fakeDesk{}, true)
	newDesktop = func() (desktops, error) { return nil, desktop.ErrNoDisplay }
	if out, _ := box(e, "desktop_windows", ""); !strings.Contains(out, "no desktop") {
		t.Errorf("no desktop: %q", out)
	}
	// The desktop is reached once and kept.
	fd := &fakeDesk{}
	e, _ = deskAgent(t, fd, true)
	_, _ = box(e, "desktop_windows", "")
	newDesktop = func() (desktops, error) { return nil, errors.New("reached again") }
	out, _ := box(e, "desktop_focus", "Firefox")
	if !strings.Contains(out, "[done focus]") || fd.reqs[1].Window != "Firefox" {
		t.Errorf("kept: %q", out)
	}
}

func TestDesktopRequest(t *testing.T) {
	cases := map[string]desktop.Request{
		desktop.Type:   {Kind: desktop.Type, Text: " two  words\n"},
		desktop.Key:    {Kind: desktop.Key, Keys: "alt+tab"},
		desktop.Focus:  {Kind: desktop.Focus, Window: "Notes"},
		desktop.Scroll: {Kind: desktop.Scroll, X: 1, Y: 2, Amount: -3},
		desktop.Shot:   {Kind: desktop.Shot},
	}
	commands := map[string]string{
		desktop.Type: " two  words\n", desktop.Key: " alt+tab ", desktop.Focus: "Notes",
		desktop.Scroll: "x: 1\ny: 2\namount: -3", desktop.Shot: "",
	}
	for step, want := range cases {
		got, err := desktopRequest(step, commands[step])
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", step, got, err)
		}
	}
	if _, err := desktopRequest(desktop.Scroll, "x: 1\ny: 2"); err == nil {
		t.Error("a scroll without amount: no error")
	}
}

func TestDesktopQuestionSaysWhatHappens(t *testing.T) {
	want := map[string]string{
		desktop.Shot: "picture of your screen", desktop.Windows: "titles", desktop.Type: `types "hola"`,
		desktop.Key: "presses ctrl+c", desktop.Focus: "brings Notes", desktop.Click: "mouse (click) at 3,4",
	}
	r := desktop.Request{Text: "hola", Keys: "ctrl+c", Window: "Notes", X: 3, Y: 4}
	for kind, part := range want {
		r.Kind = kind
		if q := desktopQuestion(r); !strings.Contains(q, part) {
			t.Errorf("%s: %s", kind, q)
		}
	}
}

func TestDesktopDocsOnlyWhenOn(t *testing.T) {
	a := &Agent{cfg: config.Config{}}
	if a.desktopDocs() != "" {
		t.Fatal("off")
	}
	a.cfg.Agent.DesktopControl = true
	if !strings.Contains(a.desktopDocs(), "desktop_click") {
		t.Fatal("on")
	}
	a.isChild = true
	if a.desktopDocs() != "" {
		t.Fatal("child")
	}
}

func TestTheExecutePhaseShowsThePicture(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true"}, nil)
	a := e.agent
	a.desk.shot = &shotImage
	tmpl := config.Template{User: "hi"}
	if _, err := a.ask(context.Background(), tmpl, nil, "plan"); err != nil || strings.Contains(body, "image_url") {
		t.Fatalf("another phase does not take it: %v %s", err, body)
	}
	if _, err := a.ask(context.Background(), tmpl, nil, "execute"); err != nil || !strings.Contains(body, "image_url") {
		t.Fatalf("the round takes it: %v %s", err, body)
	}
}

func TestNewDesktopNeedsAGraphicalSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the session is read from DISPLAY and WAYLAND_DISPLAY on Linux")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if _, err := newDesktop(); !errors.Is(err, desktop.ErrNoDisplay) {
		t.Fatalf("%v", err)
	}
}
