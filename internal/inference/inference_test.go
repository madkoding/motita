package inference

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func store(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "sub", "inference-servers.json"))
}

func srv(name string) Server {
	return Server{Name: name, Kind: "llamacpp", BaseURL: "http://127.0.0.1:8080/v1", Model: "qwen"}
}

func TestKinds(t *testing.T) {
	for _, k := range Kinds {
		if got, ok := KindOf(k.ID); !ok || got != k {
			t.Errorf("KindOf(%q) = %+v %v", k.ID, got, ok)
		}
		if k.Provider != "custom" && k.Provider != "ollama" {
			t.Errorf("%s speaks %q", k.ID, k.Provider)
		}
	}
	if _, ok := KindOf("nope"); ok {
		t.Error("an unknown kind was found")
	}
}

func TestSlugKeyAndID(t *testing.T) {
	cases := map[string]string{"Mi GPU #2": "mi-gpu-2", "  --A--  ": "a", "!!!": "", strings.Repeat("ab", 30): strings.Repeat("ab", 20)}
	for in, want := range cases {
		if got := SlugOf(in); got != want {
			t.Errorf("SlugOf(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SlugOf(strings.Repeat("a", 39) + " b"); got != strings.Repeat("a", 39) {
		t.Errorf("a cut at a dash keeps no dash: %q", got)
	}
	if got := KeyVariable("mi-gpu-2"); got != "MOTITA_INFERENCE_MI_GPU_2_API_KEY" {
		t.Errorf("KeyVariable = %q", got)
	}
	if !ValidID("mi-gpu-2") || ValidID("Mi") || ValidID("a--b") || ValidID("") || ValidID(strings.Repeat("a", 41)) {
		t.Error("ValidID gave a wrong answer")
	}
}

func TestCheck(t *testing.T) {
	bad := map[string]Server{
		"name":     {Kind: "ollama"},
		"most 80":  {Name: strings.Repeat("x", 81), Kind: "ollama"},
		"letter":   {Name: "!!!", Kind: "ollama"},
		"model id": {Name: "a", Kind: "ollama", Model: strings.Repeat("m", 201)},
		"span":     {Name: "a", Kind: "ollama", Model: "a\nb"},
		"kind":     {Name: "a", Kind: "x"},
	}
	for want, s := range bad {
		if got := Check(s); !strings.Contains(got, want) {
			t.Errorf("Check(%+v) = %q, want %q", s, got, want)
		}
	}
	if got := Check(srv("ok")); got != "" {
		t.Errorf("Check = %q", got)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := store(t)
	if !s.CanSave() {
		t.Fatal("CanSave")
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty: %v %v", list, err)
	}
	a, err := s.Add(srv("Alpha"))
	if err != nil || a.ID != "alpha" {
		t.Fatalf("add: %+v %v", a, err)
	}
	if _, err := s.Add(srv("alpha")); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := s.Add(srv("Beta")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("beta")
	if err != nil || got.Name != "Beta" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get("zzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
	up, err := s.Update("alpha", Server{ID: "other", Name: "Renamed", Kind: "ollama", BaseURL: "http://h:1/v1"})
	if err != nil || up.ID != "alpha" || up.Name != "Renamed" {
		t.Fatalf("update: %+v %v", up, err)
	}
	if _, err := s.Update("zzz", srv("x")); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if err := s.Remove("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove missing: %v", err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].ID != "beta" {
		t.Errorf("list = %+v", list)
	}
	if info, err := os.Stat(s.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode: %v %v", info, err)
	}
}

func TestStoreLimitAndUnreadable(t *testing.T) {
	s := store(t)
	for i := 0; i < MaxServers; i++ {
		if _, err := s.Add(srv("s" + strings.Repeat("a", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(srv("one more")); !errors.Is(err, ErrFull) {
		t.Errorf("full: %v", err)
	}
	if err := os.WriteFile(s.path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil {
		t.Error("a broken file listed")
	}
	if _, err := s.Add(srv("x")); err == nil {
		t.Error("a broken file was overwritten")
	}
	if _, err := s.Update("x", srv("x")); err == nil {
		t.Error("update on a broken file")
	}
	if err := s.Remove("x"); err == nil {
		t.Error("remove on a broken file")
	}
	if _, err := s.Get("x"); err == nil {
		t.Error("get on a broken file")
	}
	if err := os.WriteFile(s.path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(); err != nil || list == nil || len(list) != 0 {
		t.Errorf("null servers: %v %v", list, err)
	}
}

func TestStoreWithoutFile(t *testing.T) {
	s := NewStore("")
	if s.CanSave() {
		t.Error("CanSave")
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Errorf("list: %v %v", list, err)
	}
	if _, err := s.Add(srv("a")); err == nil {
		t.Error("saved without a file")
	}
	if PathBeside("") != "" || PathBeside("/x/motita.yaml") != "/x/inference-servers.json" {
		t.Error("PathBeside")
	}
}

func TestStoreReadFailure(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir) // a directory: reading it fails other than "not exist"
	if _, err := s.List(); err == nil {
		t.Error("a directory was read")
	}
}

func TestStoreWriteFailures(t *testing.T) {
	steps := []struct {
		name string
		hook func() func()
	}{
		{"mkdir", func() func() {
			old := mkdirAll
			mkdirAll = func(string, os.FileMode) error { return errors.New("no") }
			return func() { mkdirAll = old }
		}},
		{"temp", func() func() {
			old := createTemp
			createTemp = func(string, string) (*os.File, error) { return nil, errors.New("no") }
			return func() { createTemp = old }
		}},
		{"rename", func() func() {
			old := rename
			rename = func(string, string) error { return errors.New("no") }
			return func() { rename = old }
		}},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			s := store(t)
			undo := st.hook()
			defer undo()
			if _, err := s.Add(srv("a")); err == nil {
				t.Error("the write did not fail")
			}
			if st.name == "rename" {
				if m, _ := filepath.Glob(filepath.Join(filepath.Dir(s.path), ".motita-inference-*")); len(m) != 0 {
					t.Errorf("temporary file left: %v", m)
				}
			}
		})
	}
}

func TestWriteChmodAndCloseFailures(t *testing.T) {
	s := store(t)
	// The temporary file is removed under the writer, so the chmod after the write fails.
	old := createTemp
	createTemp = func(dir, p string) (*os.File, error) {
		f, err := old(dir, p)
		if err == nil {
			_ = os.Remove(f.Name())
		}
		return f, err
	}
	defer func() { createTemp = old }()
	if _, err := s.Add(srv("a")); err == nil {
		t.Error("chmod of a vanished file passed")
	}
}

func TestWriteToAClosedFileFails(t *testing.T) {
	s := store(t)
	old := createTemp
	createTemp = func(dir, p string) (*os.File, error) {
		f, err := old(dir, p)
		if err == nil {
			_ = f.Close()
		}
		return f, err
	}
	defer func() { createTemp = old }()
	if _, err := s.Add(srv("a")); err == nil {
		t.Error("a write to a closed file passed")
	}
}
