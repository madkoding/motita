package devbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodConfig = `{"services":{
 "web":{"networks":{"default":null},"volumes":[{"type":"bind","source":"%DIR%/src"},{"type":"volume","source":"data"}]},
 "db":{}},
 "configs":{"c":{"file":"%DIR%/c.conf"},"inline":{}},
 "secrets":{"s":{"file":"%DIR%/s.txt"}}}`

func composeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func composeRuntime(cfg string, fail map[string]bool) func([]string) ([]byte, error) {
	return func(a []string) ([]byte, error) {
		if a[0] != "compose" {
			return nil, nil
		}
		verb := ""
		for _, v := range []string{"config", "up", "logs", "down"} {
			for _, x := range a {
				if x == v {
					verb = v
				}
			}
		}
		if fail[verb] {
			return []byte(verb + " failed"), errors.New("exit 1")
		}
		if verb == "config" {
			return []byte(cfg), nil
		}
		return nil, nil
	}
}

func TestCheckCompose(t *testing.T) {
	dir := "/p"
	cases := map[string]struct{ json, want string }{
		"ok":          {`{"services":{"web":{"volumes":[{"type":"bind","source":"/p/src"}]}}}`, ""},
		"no service":  {`{"services":{"db":{}}}`, `no service "web" (it has: db)`},
		"ports":       {`{"services":{"web":{"ports":[{"published":"80"}]}}}`, "publishes ports"},
		"privileged":  {`{"services":{"web":{"privileged":true}}}`, "privileged"},
		"host net":    {`{"services":{"web":{"network_mode":"host"}}}`, "network_mode"},
		"pid":         {`{"services":{"web":{"pid":"host"}}}`, "pid, ipc"},
		"ipc":         {`{"services":{"web":{"ipc":"host"}}}`, "pid, ipc"},
		"userns":      {`{"services":{"web":{"userns_mode":"host"}}}`, "pid, ipc"},
		"caps":        {`{"services":{"web":{"cap_add":["SYS_ADMIN"]}}}`, "capabilities"},
		"devices":     {`{"services":{"web":{"devices":["/dev/sda"]}}}`, "devices"},
		"seccomp":     {`{"services":{"web":{"security_opt":["seccomp=unconfined"]}}}`, "security profile"},
		"label off":   {`{"services":{"web":{"security_opt":["label=disable"]}}}`, "security profile"},
		"other net":   {`{"services":{"web":{"networks":{"front":null}}}}`, "network front"},
		"bind out":    {`{"services":{"web":{"volumes":[{"type":"bind","source":"/etc"}]}}}`, "outside the project"},
		"bind parent": {`{"services":{"web":{"volumes":[{"type":"bind","source":"/"}]}}}`, "outside the project"},
		"config out":  {`{"services":{"web":{}},"configs":{"c":{"file":"/etc/passwd"}}}`, "outside the project"},
		"secret out":  {`{"services":{"web":{}},"secrets":{"s":{"file":"/root/.ssh/id"}}}`, "outside the project"},
	}
	for name, c := range cases {
		var cfg composeConfig
		if err := jsonUnmarshal(c.json, &cfg); err != nil {
			t.Fatal(name, err)
		}
		err := checkCompose(cfg, dir, "web")
		if !refusedAs(err, c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
}

// refusedAs reports whether err matches want: no error for an empty want, else one that says it.
func refusedAs(err error, want string) bool {
	if want == "" {
		return err == nil
	}
	return err != nil && strings.Contains(err.Error(), want)
}

func TestComposeOverride(t *testing.T) {
	m := New(Runtime{Bin: "docker"}, "ab12")
	got := m.composeOverride([]string{"web", "db"})
	for _, want := range []string{`"db":`, `"web":`, `motita.box: "1"`, `motita.owner: "ab12"`, "internal: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("override lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, `"db"`) > strings.Index(got, `"web"`) {
		t.Error("sorted")
	}
}

func TestUpComposeAndDown(t *testing.T) {
	dir := composeDir(t)
	cfg := strings.ReplaceAll(goodConfig, "%DIR%", dir)
	f := runtimeFake(composeRuntime(cfg, nil))
	f.install(t)
	m := New(Runtime{Bin: "docker"}, "ab12")
	b, err := m.Up(context.Background(), Spec{Compose: dir, Service: "web", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Internal() || b.URL != "http://web:8080" || b.network != "motita-box-ab12-1_default" {
		t.Fatalf("%+v", b)
	}
	up := findCall(f, func(a []string) bool { return a[0] == "compose" && has(a, " up ") })
	for _, want := range []string{"-p motita-box-ab12-1", "--project-directory " + dir, "override.yaml", "up -d --build"} {
		if !has(up, want) {
			t.Fatalf("%q missing: %v", want, up)
		}
	}
	data, err := os.ReadFile(b.scratch + "/override.yaml")
	if err != nil || !strings.Contains(string(data), "internal: true") {
		t.Fatalf("override: %v", err)
	}
	if err := m.Down(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	down := findCall(f, func(a []string) bool { return a[0] == "compose" && has(a, " down ") })
	if !has(down, "down -v --rmi local --remove-orphans") {
		t.Fatalf("%v", f.calls)
	}
	if _, err := os.Stat(b.scratch); !os.IsNotExist(err) {
		t.Fatal("the override folder goes with the box")
	}
}

func TestUpComposeFailures(t *testing.T) {
	dir := composeDir(t)
	cfg := strings.ReplaceAll(goodConfig, "%DIR%", dir)
	cases := []struct {
		name string
		cfg  string
		fail map[string]bool
		want string
	}{
		{"config fails", cfg, map[string]bool{"config": true}, "config --format json"},
		{"not json", "not json", nil, "JSON"},
		{"up fails", cfg, map[string]bool{"up": true}, "up failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := runtimeFake(composeRuntime(c.cfg, c.fail))
			f.install(t)
			_, err := New(Runtime{Bin: "docker"}, "x").Up(context.Background(), Spec{Compose: dir, Service: "web", Port: 80})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v, want %q", err, c.want)
			}
		})
	}
}

func TestComposeUpFailureTakesTheProjectDown(t *testing.T) {
	dir := composeDir(t)
	f := runtimeFake(composeRuntime(strings.ReplaceAll(goodConfig, "%DIR%", dir), map[string]bool{"up": true}))
	f.install(t)
	spec := Spec{Compose: dir, Service: "web", Port: 80}
	if _, err := New(Runtime{Bin: "docker"}, "x").Up(context.Background(), spec); err == nil {
		t.Fatal("up failed")
	}
	if !has(f.calls[len(f.calls)-1], " down ") {
		t.Fatalf("%v", f.calls)
	}
}

func TestARefusedComposeFileNeverRuns(t *testing.T) {
	dir := composeDir(t)
	f := runtimeFake(composeRuntime(`{"services":{"web":{"privileged":true}}}`, nil))
	f.install(t)
	_, err := New(Runtime{Bin: "docker"}, "x").Up(context.Background(), Spec{Compose: dir, Service: "web", Port: 80})
	if err == nil || f.ran("compose") != 1 {
		t.Fatalf("only the config was read: %v %v", err, f.calls)
	}
}

func TestComposeThatNeverAnswers(t *testing.T) {
	dir := composeDir(t)
	f := runtimeFake(composeRuntime(strings.ReplaceAll(goodConfig, "%DIR%", dir), nil))
	inner := f.answer
	f.answer = func(a []string) ([]byte, error) {
		if a[0] == "run" {
			return nil, errors.New("refused")
		}
		return inner(a)
	}
	f.install(t)
	c, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := New(Runtime{Bin: "docker"}, "x").Up(c, Spec{Compose: dir, Service: "web", Port: 80})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("never ready: %v", err)
	}
}

func TestComposeNeedsAFileAndAScratchFolder(t *testing.T) {
	ctx := context.Background()
	dir := composeDir(t)
	spec := Spec{Compose: dir, Service: "web", Port: 80}
	(&fake{}).install(t)
	empty := Spec{Compose: t.TempDir(), Service: "web", Port: 80}
	_, err := New(Runtime{Bin: "docker"}, "x").Up(ctx, empty)
	if err == nil || !strings.Contains(err.Error(), "no compose.yaml") {
		t.Errorf("no file: %v", err)
	}
	runtimeFake(composeRuntime(strings.ReplaceAll(goodConfig, "%DIR%", dir), nil)).install(t)
	oldTemp := tempDir
	t.Cleanup(func() { tempDir = oldTemp })
	tempDir = func(string, string) (string, error) { return "", errors.New("no temp") }
	if _, err := New(Runtime{Bin: "docker"}, "x").Up(ctx, spec); err == nil {
		t.Error("no scratch folder")
	}
	tempDir = func(string, string) (string, error) { return filepath.Join(t.TempDir(), "missing"), nil }
	if _, err := New(Runtime{Bin: "docker"}, "x").Up(ctx, spec); err == nil {
		t.Error("cannot write the override")
	}
}

func TestSpecValidationForCompose(t *testing.T) {
	for name, s := range map[string]Spec{
		"no service":      {Compose: "/p", Port: 80},
		"bad service":     {Compose: "/p", Service: "a b", Port: 80},
		"service alone":   {Image: "nginx", Service: "web", Port: 80},
		"compose + image": {Compose: "/p", Image: "nginx", Service: "web", Port: 80},
	} {
		if s.Validate() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	if err := (Spec{Compose: "/p", Service: "web", Port: 80}).Validate(); err != nil {
		t.Error(err)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
