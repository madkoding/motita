package config

import (
	"fmt"
	"path/filepath"
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
		Semantic: true, CacheDir: defaultCodeIndexDir(),
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
	return nil
}
