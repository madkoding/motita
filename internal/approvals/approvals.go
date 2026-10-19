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
	"slices"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/policy"
)

// Key is the shape of a command under a rule: what one "always allow" covers.
type Key struct {
	Rule    string `json:"rule"`
	Program string `json:"program"`
}

// KeyOf reads the shape of a one-command line under rule. It reports false when the line has no
// program to remember, so the caller asks as it always did. A line that chains commands has one
// shape per program: see KeysOf.
func KeyOf(rule, command string) (Key, bool) {
	keys, ok := KeysOf(rule, command)
	if !ok {
		return Key{}, false
	}
	return keys[0], true
}

// KeysOf reads the shape of every program of a line under rule: what "always allow" has to cover
// for the same line, or a line made of the same programs, to run without a question.
//
// A line is judged as a whole, so the question is asked about the whole line; but a person who
// said "always" to `git add . && git commit -m x && git push` meant "git push without asking",
// not "lines that start with git add". Keeping only the first program made the next line
// (`git commit --amend && git push`, `cd dir && git push`) a new shape, and "always allow" asked
// again on nearly every line a model writes. Programs the shell runs itself (cd, echo, export)
// are not what the question was about, so they are left out unless the line has nothing else.
func KeysOf(rule, command string) ([]Key, bool) {
	if rule == "" {
		return nil, false
	}
	var keys, builtins []Key
	for _, words := range policy.Commands(command) {
		k, ok := keyOfWords(rule, words)
		if !ok {
			continue
		}
		list := &keys
		if policy.IsBuiltin(k.Program) {
			list = &builtins
		}
		if !slices.Contains(*list, k) {
			*list = append(*list, k)
		}
	}
	if len(keys) == 0 {
		keys = builtins
	}
	return keys, len(keys) > 0
}

// keyOfWords is the shape of one command: its program, with the subcommand when it has one.
func keyOfWords(rule string, words []string) (Key, bool) {
	for len(words) > 0 && strings.Contains(words[0], "=") {
		words = words[1:] // leading VAR=value assignments are not the program
	}
	if len(words) == 0 {
		return Key{}, false
	}
	program := filepath.Base(words[0])
	if len(words) > 1 && withSubcommands[program] && isSubcommand(words[1]) {
		program += " " + words[1]
	}
	return Key{Rule: rule, Program: program}, true
}

// withSubcommands are the programs whose second word names what they do (git push, npm install,
// go test). For any other program a lowercase second word is an operand (`cp report /tmp/x`,
// `mkdir out`), and keeping it in the shape made "always allow" cover that one file only: the
// next `cp` of another name was asked about again.
var withSubcommands = map[string]bool{
	"git": true, "gh": true, "npm": true, "yarn": true, "pnpm": true, "bun": true, "go": true,
	"cargo": true, "rustup": true, "docker": true, "docker-compose": true, "podman": true,
	"kubectl": true, "helm": true, "terraform": true, "pip": true, "pip3": true, "pipx": true,
	"uv": true, "poetry": true, "apt": true, "apt-get": true, "brew": true, "systemctl": true,
	"dotnet": true, "gradle": true, "mvn": true, "composer": true, "gem": true, "bundle": true,
	"flatpak": true, "snap": true, "winget": true, "choco": true,
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

// AllowedAll says whether every one of keys is allowed in scope: a line is run without a question
// only when each program of it was allowed under its rule.
func (s *Store) AllowedAll(scope string, keys []Key) bool {
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		if !s.Allowed(scope, k) {
			return false
		}
	}
	return true
}

// RememberAll allows every one of keys in scope from now on, and writes the file once.
func (s *Store) RememberAll(scope string, keys []Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, k := range keys {
		if !slices.Contains(s.data[scope], k) {
			s.data[scope] = append(s.data[scope], k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
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

// marshal encodes the store; a variable so a test can make the encoding fail, which a map of
// strings never does on its own.
var marshal = json.MarshalIndent

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := marshal(s.data, "", "  ")
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
