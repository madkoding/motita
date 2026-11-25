// Package inference keeps the registry of inference servers: the machines and programs that
// serve models over HTTP (llama.cpp, LM Studio, Ollama, any OpenAI-compatible endpoint).
//
// The registry is only a list of addresses. It adds no protocol: every kind speaks one the LLM
// client already has (see Kind.Provider), so a server of the list is used by handing its address
// to that provider. The key of a server is not stored here: the gateway keeps it in the
// credentials file under KeyVariable, and the list never holds it.
package inference

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Kind is the program behind a server.
type Kind struct {
	// ID is the value a server's kind holds.
	ID string `json:"id"`
	// Name is what the user sees.
	Name string `json:"name"`
	// DefaultBaseURL is where the program listens by default; empty when there is none to guess.
	DefaultBaseURL string `json:"default_base_url"`
	// Provider is the LLM provider that speaks to it: "ollama", or "custom" for the OpenAI chat
	// protocol.
	Provider string `json:"provider"`
}

// Kinds are the programs the registry knows, in the order they are offered.
var Kinds = []Kind{
	{ID: "llamacpp", Name: "llama.cpp (llama-server)", DefaultBaseURL: "http://127.0.0.1:8080/v1", Provider: "custom"},
	{ID: "lmstudio", Name: "LM Studio", DefaultBaseURL: "http://127.0.0.1:1234/v1", Provider: "custom"},
	{ID: "ollama", Name: "Ollama", DefaultBaseURL: "http://localhost:11434/v1", Provider: "ollama"},
	{ID: "openai", Name: "OpenAI-compatible (TensorFold, vLLM, others)", Provider: "custom"},
}

// KindOf is the kind with that id.
func KindOf(id string) (Kind, bool) {
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// Limits of what a server holds.
const (
	// MaxServers is how many servers the list keeps.
	MaxServers = 32
	// MaxName is the longest name, in bytes.
	MaxName = 80
	// MaxModel is the longest model id, in bytes.
	MaxModel = 200
)

// Server is one entry of the registry.
type Server struct {
	// ID names the entry in the API and in its key variable; made from the name when it is added
	// and never changed.
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// BaseURL is the endpoint, already checked by the caller.
	BaseURL string `json:"base_url"`
	// Model is the model to ask for on this server; empty leaves the choice to the user.
	Model string `json:"model,omitempty"`
}

// Errors the store answers with; the gateway turns each into a status.
var (
	ErrNotFound  = errors.New("there is no such server")
	ErrDuplicate = errors.New("a server with that name already exists")
	ErrFull      = fmt.Errorf("the list holds at most %d servers", MaxServers)
)

var (
	slugRun = regexp.MustCompile(`[^a-z0-9]+`)
	// idShape is what an id may be: a lowercase slug.
	idShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// SlugOf is the id a name makes: lowercase letters and digits joined by dashes, "" when the name
// has none.
func SlugOf(name string) string {
	s := strings.Trim(slugRun.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// KeyVariable is the credentials-file variable that holds the key of the server with that id.
func KeyVariable(id string) string {
	return "MOTITA_INFERENCE_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_API_KEY"
}

// Check is why a server cannot be kept, or "" when it can. The address is the caller's to check.
func Check(s Server) string {
	switch {
	case strings.TrimSpace(s.Name) == "":
		return "give the server a name"
	case len(s.Name) > MaxName:
		return fmt.Sprintf("the name is at most %d characters", MaxName)
	case SlugOf(s.Name) == "":
		return "the name needs a letter or a digit"
	case len(s.Model) > MaxModel:
		return fmt.Sprintf("the model id is at most %d characters", MaxModel)
	case strings.ContainsAny(s.Model, "\r\n\x00"):
		return "the model id cannot span lines"
	}
	if _, ok := KindOf(s.Kind); !ok {
		return "the kind must be one of: " + kindIDs()
	}
	return ""
}

func kindIDs() string {
	ids := make([]string, 0, len(Kinds))
	for _, k := range Kinds {
		ids = append(ids, k.ID)
	}
	return strings.Join(ids, ", ")
}

// Store is the list, kept in a JSON file. It reads the file at each call, so a change made by
// another process shows at once; a mutex orders this process's writers.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore keeps the list in path. An empty path is a store that cannot save.
func NewStore(path string) *Store { return &Store{path: path} }

// PathBeside is where the list lives for a configuration file: in the same folder.
func PathBeside(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "inference-servers.json")
}

// CanSave says whether the store has a file to write.
func (s *Store) CanSave() bool { return s.path != "" }

type file struct {
	Servers []Server `json:"servers"`
}

// List is the servers in the order they were added. A missing file is an empty list; a file that
// does not parse is an error rather than an empty list, so a later save cannot erase it.
func (s *Store) List() ([]Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

func (s *Store) read() ([]Server, error) {
	if s.path == "" {
		return []Server{}, nil
	}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []Server{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the server list cannot be read: %w", err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("the server list cannot be read: %w", err)
	}
	if f.Servers == nil {
		f.Servers = []Server{}
	}
	return f.Servers, nil
}

// Get is the server with that id.
func (s *Store) Get(id string) (Server, error) {
	list, err := s.List()
	if err != nil {
		return Server{}, err
	}
	for _, v := range list {
		if v.ID == id {
			return v, nil
		}
	}
	return Server{}, ErrNotFound
}

// Add keeps a new server and returns it with its id. The caller has run Check.
func (s *Store) Add(v Server) (Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read()
	if err != nil {
		return Server{}, err
	}
	if len(list) >= MaxServers {
		return Server{}, ErrFull
	}
	v.ID = SlugOf(v.Name)
	for _, o := range list {
		if o.ID == v.ID {
			return Server{}, ErrDuplicate
		}
	}
	return v, s.write(append(list, v))
}

// Update replaces the server with that id by v, keeping the id.
func (s *Store) Update(id string, v Server) (Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read()
	if err != nil {
		return Server{}, err
	}
	at := -1
	for i, o := range list {
		if o.ID == id {
			at = i
		}
	}
	if at < 0 {
		return Server{}, ErrNotFound
	}
	v.ID = id
	list[at] = v
	return v, s.write(list)
}

// Remove drops the server with that id.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read()
	if err != nil {
		return err
	}
	for i, o := range list {
		if o.ID == id {
			return s.write(append(list[:i], list[i+1:]...))
		}
	}
	return ErrNotFound
}

// ValidID says whether id has the shape of an id, so a request path is never used as a variable
// name unchecked.
func ValidID(id string) bool { return len(id) <= 40 && idShape.MatchString(id) }

// The file operations, as variables so a test can make each fail.
var (
	mkdirAll   = os.MkdirAll
	createTemp = os.CreateTemp
	rename     = os.Rename
)

func (s *Store) write(list []Server) error {
	if s.path == "" {
		return errors.New("there is no configuration file to keep the server list beside")
	}
	// A list of strings always encodes.
	b, _ := json.MarshalIndent(file{Servers: list}, "", "  ")
	dir := filepath.Dir(s.path)
	if err := mkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("the server list cannot be written: %w", err)
	}
	tmp, err := createTemp(dir, ".motita-inference-*")
	if err != nil {
		return fmt.Errorf("the server list cannot be written: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o600)
	}
	if werr == nil {
		werr = rename(name, s.path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("the server list cannot be written: %w", werr)
	}
	return nil
}
