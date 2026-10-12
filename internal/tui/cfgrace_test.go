package tui

import (
	"bytes"
	"sync"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
)

// TestChangingTheModelWhileATurnReadsItIsNotARace: the model and the reasoning level are changed
// by a front end while a turn of the same session reads the configuration. SetLLM and
// SetReasoning wrote it without cfgMu, which `go test -race` reports here.
func TestChangingTheModelWhileATurnReadsItIsNotARace(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.SetLLM("", []string{"a", "b"}[i%2])
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.SetReasoning([]string{"low", "high"}[i%2])
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = r.Config()
			_, _, _ = r.engine()
		}
	}()
	wg.Wait()
	if got := r.Config().LLM.Model; got != "b" {
		t.Errorf("the last model set was b, the runner holds %q", got)
	}
}
