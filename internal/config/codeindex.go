package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/madkoding/motita/internal/codeindex"
	"github.com/madkoding/motita/internal/memory"
)

// CodeIndex is the built-in index behind the search_index tool: motita reads the workspace once,
// ranks passages by what they are about (BM25, definitions first) and answers with a few lines
// instead of whole files. It lives in memory and in this process; nothing is sent anywhere.
//
// It is ON by default because it only reads what the agent could already read. The limits keep a
// huge tree from costing memory: a file over MaxFileKB or beyond MaxFiles is not indexed, and the
// agent falls back to search_in_files for those. Files the folder's .gitignore names are skipped.
type CodeIndex struct {
	Enabled bool `yaml:"enabled"`
	// MaxFileKB skips files larger than this many KB. Zero means the built-in default.
	MaxFileKB int `yaml:"max_file_kb"`
	// MaxFiles stops indexing after this many files. Zero means the built-in default.
	MaxFiles int `yaml:"max_files"`
	// Semantic adds a second ranking by meaning, learned from the folder's own vocabulary (random
	// indexing, no model and no network), fused with the word ranking.
	Semantic bool `yaml:"semantic"`
	// CacheDir is where the index is kept between runs, so the next start does not read every
	// file again. It holds the terms of the source, not its text, and is private to the user.
	// Empty keeps the index in memory only.
	CacheDir string `yaml:"cache_dir"`
	// EmbeddingsURL, when set, adds a third ranking from a real embedding model: an
	// OpenAI-compatible endpoint (POST <url>/embeddings). OFF by default, and turning it on SENDS
	// THE TEXT of the indexed files (and every query) to that endpoint, so only point it at a
	// provider you would already show the code to. A failure is a warning, never an error.
	EmbeddingsURL   string `yaml:"embeddings_url"`
	EmbeddingsModel string `yaml:"embeddings_model"`
	// EmbeddingsAPIKey is sent to EmbeddingsURL as a bearer token. MOTITA_CODE_INDEX_EMBEDDINGS_API_KEY
	// is the better place for it.
	EmbeddingsAPIKey string `yaml:"embeddings_api_key"`
	// EmbeddingsAuto, with no EmbeddingsURL, looks for an Ollama on this machine (port 11434) that
	// has nomic-embed-text and uses it. It is on by default because the text goes to a server on
	// this same machine; nothing leaves it. Turn it off to never use embeddings unasked.
	EmbeddingsAuto bool `yaml:"embeddings_auto"`
}

// Options is the block as the index takes it.
func (c CodeIndex) Options() codeindex.Options {
	return codeindex.Options{
		MaxFileKB: c.MaxFileKB, MaxFiles: c.MaxFiles, CacheDir: c.CacheDir, Semantic: c.Semantic,
		EmbedURL: c.EmbeddingsURL, EmbedModel: c.EmbeddingsModel, EmbedKey: c.EmbeddingsAPIKey,
		EmbedAuto: c.EmbeddingsAuto,
	}
}

// MemoryEmbedder is the embeddings client the memory search adds to its recall: the one the code
// index is set up with (an endpoint of the person's, or an Ollama on this machine), when the index
// is on. nil and "" when there is none, and the memory is searched by words and its own vectors.
func (c CodeIndex) MemoryEmbedder(ctx context.Context) (memory.Embedder, string) {
	if !c.Enabled {
		return nil, ""
	}
	e, id := codeindex.Endpoint(ctx, c.Options())
	if e == nil {
		return nil, ""
	}
	return e, id
}

// OpenMemory is the person's memory, in MemoryDir, with the embeddings of the code index (ci) added
// to its recall. nil when there is no home to keep it in. Plan and task mode, the interface and
// the gateway read and write the same folder of Markdown files.
func OpenMemory(ci CodeIndex) *memory.Store {
	dir := MemoryDir()
	if dir == "" {
		return nil
	}
	return memory.Open(dir).WithEmbedder(ci.MemoryEmbedder)
}

// Defaults of the code_index block.
const (
	defaultCodeIndexMaxFileKB = 256
	defaultCodeIndexMaxFiles  = 5000
)

// defaultCodeIndex is the code_index block of the default configuration.
func defaultCodeIndex() CodeIndex {
	return CodeIndex{
		Enabled: true, MaxFileKB: defaultCodeIndexMaxFileKB, MaxFiles: defaultCodeIndexMaxFiles,
		Semantic: true, CacheDir: defaultCodeIndexDir(), EmbeddingsAuto: true,
	}
}

// defaultCodeIndexDir is inside Dir. With no home it is empty and the index is not kept.
func defaultCodeIndexDir() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "index")
	}
	return ""
}

// validateCodeIndex checks the code_index block. A disabled block is not checked, like the gateway.
func (c *Config) validateCodeIndex() error {
	if !c.CodeIndex.Enabled {
		return nil
	}
	if c.CodeIndex.MaxFileKB < 0 {
		return fmt.Errorf("code_index.max_file_kb is %d: it cannot be negative (0 means the built-in default)",
			c.CodeIndex.MaxFileKB)
	}
	if c.CodeIndex.MaxFiles < 0 {
		return fmt.Errorf("code_index.max_files is %d: it cannot be negative (0 means the built-in default)",
			c.CodeIndex.MaxFiles)
	}
	if c.CodeIndex.EmbeddingsURL == "" {
		return nil
	}
	if c.CodeIndex.EmbeddingsModel == "" {
		return errors.New("code_index.embeddings_model is empty: name the embedding model " +
			"(text-embedding-3-small on OpenAI)")
	}
	return checkKeyedEndpoint("code_index.embeddings_url", c.CodeIndex.EmbeddingsURL, c.CodeIndex.EmbeddingsAPIKey)
}
