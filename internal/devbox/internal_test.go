package devbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runtimeFake is a runtime that answers `info` and `image inspect` and leaves the rest to h.
func runtimeFake(h func(args []string) ([]byte, error)) *fake {
	return &fake{answer: func(a []string) ([]byte, error) {
		if a[0] == "info" || a[0] == "image" && len(a) > 1 && a[1] == "inspect" {
			return nil, nil
		}
		return h(a)
	}}
}

func has(args []string, want string) bool { return strings.Contains(strings.Join(args, " "), want) }

func findCall(f *fake, pred func([]string) bool) []string {
	for _, c := range f.calls {
		if pred(c[1:]) {
			return c[1:]
		}
	}
	return nil
}

// internalBox is a manager holding one internal box that was never started, for the tests of what
// is done to a box that is up.
func internalBox() *Manager {
	m := New(Runtime{Bin: "podman"}, "x")
	m.boxes["b"] = &Box{Name: "b", URL: "http://b:80", internal: true, network: "n"}
	return m
}

func upInternal(t *testing.T) (*Manager, *fake, *Box) {
	t.Helper()
	f := runtimeFake(func(a []string) ([]byte, error) {
		if a[0] == "run" && has(a, "--rm") && has(a, "-i") {
			return []byte("HTTP/1.1 200 OK\r\ncontent-type: text/html\r\n\r\n<h1>hi</h1>"), nil
		}
		return nil, nil
	})
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "ab12")
	b, err := m.Up(context.Background(), Spec{Image: "nginx:alpine", Port: 80, Internal: true})
	if err != nil {
		t.Fatal(err)
	}
	return m, f, b
}

func TestInternalBoxPublishesNothing(t *testing.T) {
	_, f, b := upInternal(t)
	if !b.Internal() || b.URL != "http://motita-box-ab12-1:80" {
		t.Fatalf("%+v", b)
	}
	net := findCall(f, func(a []string) bool { return a[0] == "network" && a[1] == "create" })
	if !has(net, "--internal") || !has(net, "motita.box=1") || net[len(net)-1] != "motita-net-ab12-1" {
		t.Fatalf("network %v", net)
	}
	run := findCall(f, func(a []string) bool { return a[0] == "run" && a[1] == "-d" })
	if has(run, " -p ") || has(run, "--publish") || !has(run, "--network motita-net-ab12-1") {
		t.Fatalf("an internal box publishes nothing: %v", run)
	}
}

func TestInternalBoxIsReadFromAHelperOnItsNetwork(t *testing.T) {
	m, f, _ := upInternal(t)
	out, err := m.Get(context.Background(), "", "/x?y=1")
	if err != nil || !strings.Contains(out, "HTTP/1.1 200 OK") {
		t.Fatalf("%v %q", err, out)
	}
	curl := findCall(f, func(a []string) bool { return a[0] == "run" && has(a, "-sS -i") })
	if !has(curl, "--network motita-net-ab12-1") || !has(curl, "--rm") || !has(curl, "curlimages/curl") ||
		curl[len(curl)-1] != "http://motita-box-ab12-1:80/x?y=1" {
		t.Fatalf("curl %v", curl)
	}
	if f.ran("pull") != 0 {
		t.Fatalf("a present image is not pulled: %v", f.calls)
	}
}

func TestInternalBoxGoesWithItsNetwork(t *testing.T) {
	m, f, _ := upInternal(t)
	if err := m.Down(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if f.ran("network") != 2 || f.ran("rm") != 1 {
		t.Fatalf("down removes the container and the network: %v", f.calls)
	}
}

func TestInternalBoxBuildsAndRemovesItsImage(t *testing.T) {
	f := runtimeFake(func([]string) ([]byte, error) { return nil, nil })
	f.install(t)
	m := New(Runtime{Bin: "docker"}, "ab12")
	if _, err := m.Up(context.Background(), Spec{Dir: "/w/app", Port: 3000, Internal: true}); err != nil {
		t.Fatal(err)
	}
	m.DownAll()
	if f.ran("build") != 1 || f.ran("rmi") != 1 {
		t.Fatalf("%v", f.calls)
	}
}

func TestInternalStartFailures(t *testing.T) {
	fails := func(verb, sub, out string) func([]string) ([]byte, error) {
		return func(a []string) ([]byte, error) {
			if a[0] == verb && (sub == "" || a[1] == sub) {
				return []byte(out), errors.New("exit 1")
			}
			return nil, nil
		}
	}
	cases := map[string]struct {
		answer func([]string) ([]byte, error)
		want   string
		last   string // the last verb run: what cleanup did
	}{
		"network": {fails("network", "create", "denied"), "denied", "rm"},
		"run":     {fails("run", "", "no such image"), "no such image", "network"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := runtimeFake(c.answer)
			f.install(t)
			spec := Spec{Image: "app", Port: 80, Internal: true}
			_, err := New(Runtime{Bin: "podman"}, "x").Up(context.Background(), spec)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v", err)
			}
			if got := f.calls[len(f.calls)-1][1]; got != c.last {
				t.Fatalf("cleanup ended with %q: %v", c.last, f.calls)
			}
		})
	}
}

func TestInternalBoxThatNeverAnswersShowsItsLog(t *testing.T) {
	f := runtimeFake(func(a []string) ([]byte, error) {
		switch {
		case a[0] == "run" && has(a, "--rm"):
			return nil, errors.New("connection refused")
		case a[0] == "logs":
			return []byte("crashed"), nil
		default:
			return nil, nil
		}
	})
	f.install(t)
	c, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := New(Runtime{Bin: "podman"}, "x").Up(c, Spec{Image: "app", Port: 80, Internal: true})
	if err == nil || !strings.Contains(err.Error(), "crashed") {
		t.Fatalf("%v", err)
	}
}

func TestHelperImageThatCannotBePulled(t *testing.T) {
	f := &fake{answer: func(a []string) ([]byte, error) {
		if a[0] == "image" || a[0] == "pull" {
			return []byte("unauthorized"), errors.New("exit 1")
		}
		return nil, nil
	}}
	f.install(t)
	m := internalBox()
	_, err := m.Get(context.Background(), "", "/")
	if err == nil || !strings.Contains(err.Error(), "could not be pulled") {
		t.Fatalf("%v", err)
	}
	if err := m.Shot(context.Background(), "", "/", filepath.Join(t.TempDir(), "a.png"), 800, 600); err == nil {
		t.Fatal("shot needs the helper image too")
	}
}

func TestHelperImagesArePulledOnceAndOverridable(t *testing.T) {
	f := &fake{answer: func(a []string) ([]byte, error) {
		if a[0] == "image" {
			return nil, errors.New("not here")
		}
		return nil, nil
	}}
	f.install(t)
	t.Setenv("MOTITA_BOX_CURL_IMAGE", "registry.local/curl:1")
	m := internalBox()
	for i := 0; i < 2; i++ {
		if _, err := m.Get(context.Background(), "", "/"); err != nil {
			t.Fatal(err)
		}
	}
	pull := findCall(f, func(a []string) bool { return a[0] == "pull" })
	if f.ran("pull") != 1 || pull[1] != "registry.local/curl:1" {
		t.Fatalf("%v", f.calls)
	}
}

func TestGetInternalFailureAndCut(t *testing.T) {
	fail := false
	f := runtimeFake(func(a []string) ([]byte, error) {
		if a[0] == "run" && has(a, "-sS") {
			if fail {
				return []byte("curl: (7) refused"), errors.New("exit 7")
			}
			return []byte(strings.Repeat("a", bodyLimit+50)), nil
		}
		return nil, nil
	})
	f.install(t)
	m := internalBox()
	out, err := m.Get(context.Background(), "", "/")
	if err != nil || !strings.Contains(out, "[body cut at 4096 bytes]") {
		t.Fatalf("%v", err)
	}
	fail = true
	if _, err := m.Get(context.Background(), "", "/"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("%v", err)
	}
}

// browserContainer fakes the browser container: what its run and the copy out of it do.
type browserContainer struct {
	runErr, cpErr error
	empty         bool
}

func (b browserContainer) answer(a []string) ([]byte, error) {
	switch {
	case a[0] == "run" && has(a, "--screenshot"):
		if b.runErr != nil {
			return []byte("boom"), b.runErr
		}
	case a[0] == "cp":
		if b.cpErr != nil {
			return []byte("no such file"), b.cpErr
		}
		content := []byte("png")
		if b.empty {
			content = nil
		}
		return nil, os.WriteFile(a[2], content, 0o644)
	}
	return nil, nil
}

func TestShotInsideCopiesThePictureOut(t *testing.T) {
	out := filepath.Join(t.TempDir(), "shots", "a.png")
	f := runtimeFake(browserContainer{}.answer)
	f.install(t)
	m := internalBox()
	if err := m.Shot(context.Background(), "", "/p", out, 1024, 768); err != nil {
		t.Fatal(err)
	}
	run := findCall(f, func(a []string) bool { return a[0] == "run" && has(a, "--screenshot") })
	if !has(run, "--network n") || !has(run, "--window-size=1024,768") || !has(run, "zenika/alpine-chrome") ||
		run[len(run)-1] != "http://b:80/p" {
		t.Fatalf("%v", run)
	}
	cp := findCall(f, func(a []string) bool { return a[0] == "cp" })
	if cp[1] != "b-shot:/tmp/shot.png" || cp[2] != out {
		t.Fatalf("%v", cp)
	}
	if f.calls[len(f.calls)-1][1] != "rm" {
		t.Fatalf("the browser container is removed: %v", f.calls)
	}
}

func TestShotInsideFailures(t *testing.T) {
	exit := errors.New("exit 1")
	cases := map[string]struct {
		b    browserContainer
		want string
	}{
		"the browser fails": {browserContainer{runErr: exit}, "boom"},
		"the copy fails":    {browserContainer{cpErr: exit}, "no such file"},
		"an empty picture":  {browserContainer{empty: true}, "no picture"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := runtimeFake(c.b.answer)
			f.install(t)
			out := filepath.Join(t.TempDir(), "a.png")
			if err := internalBox().Shot(context.Background(), "", "/p", out, 1024, 768); err == nil ||
				!strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want %q", err, c.want)
			}
		})
	}
}
