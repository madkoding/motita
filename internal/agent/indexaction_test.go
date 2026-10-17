package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// indexRound is a round over a workspace holding one Go file.
func indexRound(t *testing.T, ctx context.Context, mutate func(*config.Config)) (*roundRun, string) {
	t.Helper()
	dir := t.TempDir()
	src := "package wal\n\n// rotateSegment closes the journal segment and opens the next one.\nfunc rotateSegment() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "wal.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Agent.WorkspaceDir = dir
	cfg.CodeIndex.CacheDir = ""
	if mutate != nil {
		mutate(&cfg)
	}
	a := &Agent{cfg: cfg, log: logx.Global()}
	return &roundRun{a: a, ctx: ctx}, dir
}

func TestSearchIndexActionAnswersFromTheWorkspace(t *testing.T) {
	rr, _ := indexRound(t, context.Background(), nil)
	if rr.indexAction("command", Command{}) {
		t.Fatal("only search_index is this action")
	}
	if !rr.indexAction("search_index", Command{Description: "find it", Command: "rotateSegment"}) {
		t.Fatal("search_index must be handled")
	}
	out := rr.sb.String()
	if !strings.Contains(out, "[search_index] find it") || !strings.Contains(out, "wal.go:") {
		t.Errorf("output = %q", out)
	}
}

func TestSearchIndexActionExplainsWhyItCannotAnswer(t *testing.T) {
	rr, _ := indexRound(t, context.Background(), func(c *config.Config) { c.CodeIndex.Enabled = false })
	if out := rr.a.searchIndex(rr, "x"); !strings.Contains(out, "switched off") {
		t.Errorf("disabled: %q", out)
	}
	rr, _ = indexRound(t, context.Background(), nil)
	if out := rr.a.searchIndex(rr, "  "); !strings.Contains(out, "'command' must hold") {
		t.Errorf("empty query: %q", out)
	}
	rr, _ = indexRound(t, context.Background(), func(c *config.Config) {
		loop := filepath.Join(c.Agent.WorkspaceDir, "loop")
		if err := os.Symlink(loop, loop); err != nil {
			t.Fatal(err)
		}
		c.Agent.WorkspaceDir = loop
	})
	if out := rr.a.searchIndex(rr, "x"); !strings.HasPrefix(out, "Error: ") {
		t.Errorf("no workspace: %q", out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rr, _ = indexRound(t, ctx, nil)
	if out := rr.a.searchIndex(rr, "x"); !strings.HasPrefix(out, "Error searching the index") {
		t.Errorf("cancelled: %q", out)
	}
}

func TestSearchIndexActionReportsAnEmbeddingsFailure(t *testing.T) {
	rr, _ := indexRound(t, context.Background(), func(c *config.Config) {
		c.CodeIndex.EmbeddingsURL = "http://127.0.0.1:1"
		c.CodeIndex.EmbeddingsModel = "m"
	})
	out := rr.a.searchIndex(rr, "rotateSegment")
	if !strings.Contains(out, "embeddings unavailable") || !strings.Contains(out, "wal.go") {
		t.Errorf("out = %q", out)
	}
}

func TestSearchIndexIsListedInTheRefusalAndTheirPrompt(t *testing.T) {
	rr, _ := indexRound(t, context.Background(), nil)
	rr.unknownKind(0, "read_file", Command{Command: "x"})
	if !strings.Contains(rr.sb.String(), "search_index") {
		t.Errorf("the refusal must list search_index: %q", rr.sb.String())
	}
}
