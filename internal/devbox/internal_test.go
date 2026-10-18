package devbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// answers builds a runtime fake: a map from the verb (and sub-verb for run/network) to its answer.
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

func TestInternalBoxPublishesNothingAndIsReachedFromHelpers(t *testing.T) {
	f := runtimeFake(func(a []string) ([]byte, error) {
		if a[0] == "run" && has(a, "--rm") {
			if has(a, "-i") {
				return []byte("HTTP/1.1 200 OK\r\ncontent-type: text/html\r\n\r\n<h1>hi</h1>"), nil
			}
			return nil, nil
		}
		return nil, nil
	})
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "ab12")
	b, err := m.Up(context.Background(), Spec{Image: "nginx:alpine", Port: 80, Internal: true})
	if err != nil {
		t.Fatal(err)
	}
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

	out, err := m.Get(context.Background(), "", "/x?y=1")
	if err != nil || !strings.Contains(out, "HTTP/1.1 200 OK") {
		t.Fatalf("%v %q", err, out)
	}
	curl := findCall(f, func(a []string) bool { return a[0] == "run" && has(a, "-sS -i") })
	if !has(curl, "--network motita-net-ab12-1") || !has(curl, "--rm") || curl[len(curl)-1] != "http://motita-box-ab12-1:80/x?y=1" ||
		!has(curl, "curlimages/curl") {
		t.Fatalf("curl %v", curl)
	}
	if f.ran("pull") != 0 {
		t.Fatalf("a present image is not pulled: %v", f.calls)
	}

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

func TestInternalFailures(t *testing.T) {
	ctx := context.Background()
	spec := Spec{Image: "app", Port: 80, Internal: true}
	t.Run("network", func(t *testing.T) {
		f := runtimeFake(func(a []string) ([]byte, error) {
			if a[0] == "network" && a[1] == "create" {
				return []byte("denied"), errors.New("exit 1")
			}
			return nil, nil
		})
		f.install(t)
		_, err := New(Runtime{Bin: "podman"}, "x").Up(ctx, spec)
		if err == nil || !strings.Contains(err.Error(), "denied") || f.ran("run") != 0 {
			t.Fatalf("%v %v", err, f.calls)
		}
	})
	t.Run("run", func(t *testing.T) {
		f := runtimeFake(func(a []string) ([]byte, error) {
			if a[0] == "run" {
				return []byte("no such image"), errors.New("exit 125")
			}
			return nil, nil
		})
		f.install(t)
		_, err := New(Runtime{Bin: "podman"}, "x").Up(ctx, spec)
		if err == nil || f.calls[len(f.calls)-1][1] != "network" {
			t.Fatalf("the network made for it goes too: %v %v", err, f.calls)
		}
	})
	t.Run("never ready", func(t *testing.T) {
		f := runtimeFake(func(a []string) ([]byte, error) {
			if a[0] == "run" && has(a, "--rm") {
				return nil, errors.New("connection refused")
			}
			if a[0] == "logs" {
				return []byte("crashed"), nil
			}
			return nil, nil
		})
		f.install(t)
		c, cancel := context.WithTimeout(ctx, 30*1e6)
		defer cancel()
		_, err := New(Runtime{Bin: "podman"}, "x").Up(c, spec)
		if err == nil || !strings.Contains(err.Error(), "crashed") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("helper image missing", func(t *testing.T) {
		f := &fake{answer: func(a []string) ([]byte, error) {
			if a[0] == "image" || a[0] == "pull" {
				return []byte("unauthorized"), errors.New("exit 1")
			}
			return nil, nil
		}}
		f.install(t)
		m := New(Runtime{Bin: "podman"}, "x")
		m.boxes["b"] = &Box{Name: "b", URL: "http://b:80", internal: true, network: "n"}
		if _, err := m.Get(ctx, "", "/"); err == nil || !strings.Contains(err.Error(), "could not be pulled") {
			t.Fatalf("%v", err)
		}
		if err := m.Shot(ctx, "", "/", filepath.Join(t.TempDir(), "a.png"), 800, 600); err == nil {
			t.Fatal("shot needs the helper image too")
		}
	})
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
	m := New(Runtime{Bin: "podman"}, "x")
	m.boxes["b"] = &Box{Name: "b", URL: "http://b:80", internal: true, network: "n"}
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
	big := strings.Repeat("a", bodyLimit+50)
	fail := false
	f := runtimeFake(func(a []string) ([]byte, error) {
		if a[0] == "run" && has(a, "-sS") {
			if fail {
				return []byte("curl: (7) refused"), errors.New("exit 7")
			}
			return []byte(big), nil
		}
		return nil, nil
	})
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "x")
	m.boxes["b"] = &Box{Name: "b", URL: "http://b:80", internal: true, network: "n"}
	out, err := m.Get(context.Background(), "", "/")
	if err != nil || !strings.Contains(out, "[body cut at 4096 bytes]") {
		t.Fatalf("%v", err)
	}
	fail = true
	if _, err := m.Get(context.Background(), "", "/"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("%v", err)
	}
}

func TestShotInsideCopiesThePictureOut(t *testing.T) {
	out := filepath.Join(t.TempDir(), "shots", "a.png")
	step := ""
	f := runtimeFake(func(a []string) ([]byte, error) {
		switch {
		case a[0] == "run" && has(a, "--screenshot"):
			if step == "run fails" {
				return []byte("boom"), errors.New("exit 1")
			}
		case a[0] == "cp":
			if step == "cp fails" {
				return []byte("no such file"), errors.New("exit 1")
			}
			if step != "empty" {
				return nil, os.WriteFile(a[2], []byte("png"), 0o644)
			}
			return nil, os.WriteFile(a[2], nil, 0o644)
		}
		return nil, nil
	})
	f.install(t)
	m := New(Runtime{Bin: "podman"}, "x")
	m.boxes["b"] = &Box{Name: "b", URL: "http://b:80", internal: true, network: "n"}
	if err := m.Shot(context.Background(), "", "/p", out, 1024, 768); err != nil {
		t.Fatal(err)
	}
	run := findCall(f, func(a []string) bool { return a[0] == "run" && has(a, "--screenshot") })
	if !has(run, "--network n") || !has(run, "--window-size=1024,768") || run[len(run)-1] != "http://b:80/p" || !has(run, "zenika/alpine-chrome") {
		t.Fatalf("%v", run)
	}
	cp := findCall(f, func(a []string) bool { return a[0] == "cp" })
	if cp[1] != "b-shot:/tmp/shot.png" || cp[2] != out {
		t.Fatalf("%v", cp)
	}
	if f.calls[len(f.calls)-1][1] != "rm" {
		t.Fatalf("the browser container is removed: %v", f.calls)
	}
	for name, want := range map[string]string{"run fails": "boom", "cp fails": "no such file", "empty": "no picture"} {
		step = name
		if err := m.Shot(context.Background(), "", "/p", out, 1024, 768); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}
