package tui

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/procedures"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/skills"
)

// TestAnInstalledStoreIsTheOneTheRunnerUses: the gateway hands every conversation the same library
// and ledger, because the ledger is one file with one in-memory truth. A runner that built its own
// would let two conversations save over each other and a verdict would be lost with nothing to
// show for it.
func TestAnInstalledStoreIsTheOneTheRunnerUses(t *testing.T) {
	lg, err := logx.New(logx.Options{Path: filepath.Join(t.TempDir(), "log.txt"), Level: logx.Warn})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	r := NewAppRunner(io.Discard, io.Discard, config.Default(), nil, nil, lg)
	store := &procedures.Store{
		Library: skills.New(t.TempDir()),
		Ledger:  &reward.Ledger{Scores: map[string]reward.Score{}},
	}

	r.UseStore(store)

	if got := r.procedures(); got != store {
		t.Fatal("the runner built its own store instead of using the one it was given")
	}
	// And it keeps using it: the store is resolved once, so a second call must not replace it.
	if got := r.procedures(); got != store {
		t.Fatal("the installed store was replaced on a later turn")
	}
}

// TestARunnerWithNoStoreStillBuildsOne: every other caller - the command line's task and plan runs
// - has one conversation and no reason to share, so a runner given nothing must still work.
func TestARunnerWithNoStoreStillBuildsOne(t *testing.T) {
	cfg := config.Default()
	cfg.Skills.Dir = t.TempDir()
	lg2, err := logx.New(logx.Options{Path: filepath.Join(t.TempDir(), "log.txt"), Level: logx.Warn})
	if err != nil {
		t.Fatalf("logx.New: %v", err)
	}
	r := NewAppRunner(io.Discard, io.Discard, cfg, nil, nil, lg2)

	if got := r.procedures(); got == nil {
		t.Fatal("a runner with no installed store must still resolve one on first use")
	}
}
