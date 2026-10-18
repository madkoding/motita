// Package approvals keeps the commands a person said to always allow in a project, so the same
// kind of command is not asked about again in every session of that project. It remembers the
// SHAPE of a command (the policy rule it fell under and the program, with its subcommand when
// it has one), never the whole line: "always allow `git push` here" is a decision a person can
// make once; one about every future line is not.
package approvals

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Key is the shape of a command under a rule: what one "always allow" covers.
type Key struct {
	Rule    string `json:"rule"`
	Program string `json:"program"`
}

// KeyOf reads the shape of command under rule. It reports false when the line has no program to
// remember, so the caller asks as it always did.
func KeyOf(rule, command string) (Key, bool) {
	words := strings.Fields(command)
	for len(words) > 0 && strings.Contains(words[0], "=") {
		words = words[1:] // leading VAR=value assignments are not the program
	}
	if len(words) == 0 || rule == "" {
		return Key{}, false
	}
	program := filepath.Base(words[0])
	if len(words) > 1 && isSubcommand(words[1]) {
		program += " " + words[1]
	}
	return Key{Rule: rule, Program: program}, true
}

// isSubcommand says a second word names what the program does (git push, npm install, go test)
// rather than an option, a path or a value.
func isSubcommand(w string) bool {
	if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "/.=~$\"'`|;&<>*") {
		return false
	}
	for _, r := range w {
		if (r < 'a' || r > 'z') && r != '-' && r != ':' {
			return false
		}
	}
	return true
}

// Store is the file of remembered shapes, by scope (a project, or a workspace).
type Store struct {
	mu   sync.Mutex
	path string
	data map[string][]Key
}

// Open reads the store at path, or starts an empty one when the file does not exist yet. An
// empty path keeps the store in memory, for a gateway with nowhere to persist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: map[string][]Key{}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, err
	}
	if s.data == nil {
		s.data = map[string][]Key{}
	}
	return s, nil
}

// Allowed says whether the person already allowed this shape in scope.
func (s *Store) Allowed(scope string, k Key) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.data[scope] {
		if have == k {
			return true
		}
	}
	return false
}

// Remember allows k in scope from now on, and writes the file.
func (s *Store) Remember(scope string, k Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.data[scope] {
		if have == k {
			return nil
		}
	}
	s.data[scope] = append(s.data[scope], k)
	return s.saveLocked()
}

// Forget drops everything allowed in scope, and writes the file.
func (s *Store) Forget(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[scope]; !ok {
		return nil
	}
	delete(s.data, scope)
	return s.saveLocked()
}

// List is what scope allows, in the order it was allowed.
func (s *Store) List(scope string) []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Key(nil), s.data[scope]...)
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
