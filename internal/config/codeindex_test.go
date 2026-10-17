package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/codeindex"
)

func TestCodeIndexIsOnByDefault(t *testing.T) {
	c := Default()
	if !c.CodeIndex.Enabled || c.CodeIndex.MaxFileKB != 256 || c.CodeIndex.MaxFiles != 5000 {
		t.Fatalf("default code_index = %+v", c.CodeIndex)
	}
	if !c.CodeIndex.Semantic {
		t.Error("the semantic ranking is on by default")
	}
	if err := c.ValidateWithoutKey(); err != nil {
		t.Errorf("the default must validate: %v", err)
	}
}

func TestCodeIndexIsKeptInsideTheConfigDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	if got := defaultCodeIndexDir(); got != filepath.Join(Dir(), "index") || got == "" {
		t.Errorf("dir = %q", got)
	}
}

func TestValidateCodeIndex(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CodeIndex)
		want   string
	}{
		{"zero means default", func(x *CodeIndex) { x.MaxFileKB, x.MaxFiles = 0, 0 }, ""},
		{"negative size", func(x *CodeIndex) { x.MaxFileKB = -1 }, "code_index.max_file_kb"},
		{"negative files", func(x *CodeIndex) { x.MaxFiles = -1 }, "code_index.max_files"},
		{"embeddings need a model", func(x *CodeIndex) { x.EmbeddingsURL = "https://api.example.com/v1" },
			"code_index.embeddings_model"},
		{"embeddings need an http url", func(x *CodeIndex) { x.EmbeddingsURL, x.EmbeddingsModel = "ftp://x", "m" },
			"code_index.embeddings_url"},
		{"embeddings key not in clear text", func(x *CodeIndex) {
			x.EmbeddingsURL, x.EmbeddingsModel, x.EmbeddingsAPIKey = "http://example.com/v1", "m", "k"
		}, "clear text"},
		{"embeddings configured", func(x *CodeIndex) {
			x.EmbeddingsURL, x.EmbeddingsModel, x.EmbeddingsAPIKey = "https://api.example.com/v1", "m", "k"
		}, ""},
		{"off is not checked", func(x *CodeIndex) { x.Enabled, x.MaxFiles = false, -1 }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c.CodeIndex)
			err := c.ValidateWithoutKey()
			if tc.want == "" && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCodeIndexOptions(t *testing.T) {
	c := CodeIndex{
		MaxFileKB: 1, MaxFiles: 2, CacheDir: "/c", Semantic: true,
		EmbeddingsURL: "u", EmbeddingsModel: "m", EmbeddingsAPIKey: "k",
	}
	want := codeindex.Options{
		MaxFileKB: 1, MaxFiles: 2, CacheDir: "/c", Semantic: true, EmbedURL: "u", EmbedModel: "m", EmbedKey: "k",
	}
	if got := c.Options(); got != want {
		t.Errorf("Options() = %+v", got)
	}
}
