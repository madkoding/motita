package devbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const psOut = "abc123def456|motita-box-x-1|nginx:alpine|Up 2 minutes|2026-10-18 14:00:00 +0000 UTC\n" +
	"fff000aaa111|motita-box-x-1-shot|zenika/alpine-chrome|Exited (0) 1 minute ago|2026-10-18 14:01:00 +0000 UTC\n" +
	"garbage line\n"

func adminFake(t *testing.T, psErr error, rmFail string) *fake {
	f := &fake{answer: func(a []string) ([]byte, error) {
		switch a[0] {
		case "ps":
			return []byte(psOut), psErr
		case "rm":
			if a[2] == rmFail {
				return []byte("busy"), errors.New("exit 1")
			}
		}
		return nil, nil
	}}
	f.install(t)
	return f
}

func TestActiveListsOnlyOurContainers(t *testing.T) {
	f := adminFake(t, nil, "")
	bin, list, err := Active(context.Background())
	if err != nil || bin != "podman" || len(list) != 2 {
		t.Fatalf("%v %v %v", bin, list, err)
	}
	if list[0] != (Info{ID: "abc123def456", Name: "motita-box-x-1", Image: "nginx:alpine", Status: "Up 2 minutes", Created: "2026-10-18 14:00:00 +0000 UTC"}) {
		t.Fatalf("%+v", list[0])
	}
	ps := findCall(f, func(a []string) bool { return a[0] == "ps" })
	if !has(ps, "--filter label=motita.box") || !has(ps, "-a") {
		t.Fatalf("%v", ps)
	}
	adminFake(t, errors.New("daemon"), "")
	if _, _, err := Active(context.Background()); err == nil || !strings.Contains(err.Error(), "could not list") {
		t.Fatalf("%v", err)
	}
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	if bin, list, err := Active(context.Background()); bin != "" || list != nil || err != nil {
		t.Fatalf("no runtime is an empty list: %v %v %v", bin, list, err)
	}
}

func TestRemoveOnlyOurs(t *testing.T) {
	f := adminFake(t, nil, "")
	ctx := context.Background()
	if err := Remove(ctx, "motita-box-x-1"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, "fff000"); err != nil {
		t.Fatalf("an id prefix: %v", err)
	}
	if rm := findCall(f, func(a []string) bool { return a[0] == "rm" }); rm[2] != "abc123def456" {
		t.Fatalf("%v", rm)
	}
	if f.ran("network") != 2 || f.ran("image") < 2 {
		t.Fatalf("it tidies the networks and images of ours: %v", f.calls)
	}
	for _, ref := range []string{"", "someone-elses-db", "abc", "motita"} {
		if err := Remove(ctx, ref); err == nil {
			t.Errorf("%q must be refused", ref)
		}
	}
	if err := Remove(ctx, "someone-elses-db"); !errors.Is(err, ErrNotOurs) {
		t.Errorf("%v", err)
	}
	adminFake(t, nil, "abc123def456")
	if err := Remove(ctx, "motita-box-x-1"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("%v", err)
	}
	adminFake(t, errors.New("daemon"), "")
	if err := Remove(ctx, "motita-box-x-1"); err == nil {
		t.Fatal("cannot list")
	}
}

func TestSweepRemovesEverythingOfOurs(t *testing.T) {
	f := adminFake(t, nil, "fff000aaa111")
	if n := Sweep(context.Background()); n != 1 {
		t.Fatalf("removed %d", n)
	}
	if f.ran("network") != 1 {
		t.Fatalf("%v", f.calls)
	}
	adminFake(t, errors.New("daemon"), "")
	if n := Sweep(context.Background()); n != 0 {
		t.Fatalf("ps fails: %d", n)
	}
	f = &fake{answer: func([]string) ([]byte, error) { return nil, nil }}
	f.install(t)
	if n := Sweep(context.Background()); n != 0 || f.ran("rm") != 0 {
		t.Fatalf("nothing to sweep: %d", n)
	}
}
