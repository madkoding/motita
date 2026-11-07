package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

func TestTheUsagePanelLeavesOutADisconnectedProviderThatWasCalled(t *testing.T) {
	srv := usageServer(t)
	srv.usageTotals = usedProviders // openai was called, but has no key now
	if r := usageRow(readUsage(t, srv), "openai"); r != nil {
		t.Errorf("a disconnected provider stays in the panel: %+v", r)
	}
}

func TestClaudeCodeIsShownWhileNamedOrUsedLately(t *testing.T) {
	asked := fakeClaudeProbe(t)
	srv := usageServer(t)
	if usageRow(readUsage(t, srv), "claude-code") != nil {
		t.Error("claude-code is shown though nothing uses it")
	}
	srv.usageTotals = func() []llm.ProviderUsage {
		return []llm.ProviderUsage{{Provider: "claude-code", Calls: 1, Last: time.Now().Add(-2 * keylessRecent)}}
	}
	if usageRow(readUsage(t, srv), "claude-code") != nil {
		t.Error("a call two days old keeps claude-code")
	}
	srv.usageTotals = func() []llm.ProviderUsage {
		return []llm.ProviderUsage{{Provider: "claude-code", Calls: 1, Last: time.Now()}}
	}
	if usageRow(readUsage(t, srv), "claude-code") == nil {
		t.Error("a call a moment ago does not show claude-code")
	}
	// The panel's first read of a claude in use asks it for its quota in the background: waited
	// for here, so the test's cleanup does not race the question.
	if !probed(asked) {
		t.Error("claude in use was not asked for its quota")
	}
	srv.usageTotals = nil
	srv.opts.Service.(*fakeService).cfg.Models.Review = "claude-code/sonnet"
	if usageRow(readUsage(t, srv), "claude-code") == nil {
		t.Error("claude-code named in the models block is not shown")
	}
}

func TestProvidersInUseNamesTheDefaultAndTheModelsBlock(t *testing.T) {
	got := providersInUse(config.LLM{Provider: "openai"}, config.Models{Code: "anthropic/x", Plan: "bad"})
	if !got["openai"] || !got["anthropic"] || len(got) != 2 {
		t.Errorf("in use = %v", got)
	}
}

func TestTheQuotaCacheForgetsOneProvider(t *testing.T) {
	c := quotaCache{items: map[string]quotaEntry{"copilot|a": {}, "ollama|b": {}, "openai|c": {}}}
	c.forget("copilot")
	c.forget("ollama-cloud")
	if len(c.items) != 1 || c.items["openai|c"].at != (time.Time{}) {
		t.Errorf("left = %v", c.items)
	}
}

// forgetSetup is a gateway whose default provider (openai) has a key in every place one can sit:
// the YAML, the generic variable and its own beside the configuration, and the motita home's file.
func forgetSetup(t *testing.T) (*Server, string, string) {
	t.Helper()
	path := llmEnv(t, openAIConfig+"  api_key: sk-yaml-0123456789\n")
	for name, v := range map[string]string{"MOTITA_LLM_API_KEY": "sk-gen", "OPENAI_API_KEY": "sk-own"} {
		if err := config.SetCredential(path, name, v); err != nil {
			t.Fatal(err)
		}
	}
	home := config.File()
	if err := os.MkdirAll(filepath.Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCredential(home, "OPENAI_API_KEY", "sk-home"); err != nil {
		t.Fatal(err)
	}
	srv, _ := llmServer(t, path)
	return srv, path, home
}

func TestDisconnectingTheDefaultProviderRemovesEveryKeyAndItsUsage(t *testing.T) {
	srv, path, home := forgetSetup(t)
	srv.quotaCache.items = map[string]quotaEntry{"openai|k": {}}
	var forgot []string
	old := forgetUsage
	forgetUsage = func(id string) { forgot = append(forgot, id) }
	t.Cleanup(func() { forgetUsage = old })
	w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/openai", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got := config.KeySource(path, srv.configuredLLM(), "openai"); got != config.KeyMissing {
		t.Errorf("key after Disconnect: %s", got)
	}
	if config.YAMLAPIKey(path) != "" {
		t.Error("the YAML keeps llm.api_key")
	}
	for _, f := range []string{config.CredentialsPath(path), config.CredentialsPath(home)} {
		body, _ := os.ReadFile(f)
		if strings.Contains(string(body), "sk-") {
			t.Errorf("%s keeps a key:\n%s", f, body)
		}
	}
	if len(forgot) != 1 || forgot[0] != "openai" || len(srv.quotaCache.items) != 0 {
		t.Errorf("usage kept: %v %v", forgot, srv.quotaCache.items)
	}
}

func TestDisconnectingAnotherProviderLeavesTheDefaultsKey(t *testing.T) {
	srv, path, _ := forgetSetup(t)
	if err := config.SetCredential(path, "ANTHROPIC_API_KEY", "sk-ant-0123456789"); err != nil {
		t.Fatal(err)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/anthropic", ""); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if config.YAMLAPIKey(path) == "" || config.KeySource(path, srv.configuredLLM(), "anthropic") != config.KeyMissing {
		t.Error("the wrong key was removed")
	}
}

func TestDisconnectReportsEachFailedRemoval(t *testing.T) {
	fail := func() error { return errors.New("read-only") }
	cases := map[string]func(){
		"home file": func() { forgetStored = func(string) error { return fail() } },
		"yaml":      func() { clearYAMLKey = func(string) error { return fail() } },
		"generic": func() {
			old, n := setCredential, 0
			// The first removal of the generic variable is the OpenAI pair's; the second is the
			// default provider's.
			setCredential = func(p, name, v string) error {
				if name == genericKey {
					if n++; n == 2 {
						return fail()
					}
				}
				return old(p, name, v)
			}
		},
	}
	for name, break_ := range cases {
		srv, _, _ := forgetSetup(t)
		oldF, oldY, oldC := forgetStored, clearYAMLKey, setCredential
		break_()
		w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/openai", "")
		forgetStored, clearYAMLKey, setCredential = oldF, oldY, oldC
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

// A slow provider must not hold the cache's lock: another provider's quotas and a Disconnect that
// forgets one are answered while the first read is still waiting.
func TestASlowQuotaReadDoesNotBlockTheOtherProviders(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.cachedQuotas(context.Background(), "slow", "t", func(context.Context) ([]llm.Quota, error) {
			close(started)
			<-release
			return []llm.Quota{{Name: "a"}}, nil
		})
	}()
	<-started
	answered := make(chan []llm.Quota, 1)
	go func() {
		answered <- srv.cachedQuotas(context.Background(), "fast", "t", func(context.Context) ([]llm.Quota, error) {
			return []llm.Quota{{Name: "b"}}, nil
		})
		srv.quotaCache.forget("fast")
	}()
	select {
	case qs := <-answered:
		if len(qs) != 1 || qs[0].Name != "b" {
			t.Errorf("quotas = %v", qs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another provider waited for the slow read")
	}
	close(release)
	<-done
	if qs, ok := srv.quotaCache.fresh("slow|t"); !ok || len(qs) != 1 {
		t.Errorf("the slow answer was not kept: %v %v", qs, ok)
	}
}
