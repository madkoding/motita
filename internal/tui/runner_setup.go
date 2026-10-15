package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/onboard"
)

// The setup of this runner: the configuration wizard, the interface language, and the report of
// the models the configured provider offers.

// RunConfig runs the first-run configuration wizard.
func (r *AppRunner) RunConfig(ctx context.Context) error {
	// The same default the first run uses, so the file the wizard writes is the one the program
	// looks for next time. With no HOME it falls back to the working directory.
	// It speaks the interface's language, and writes the setting back as it was.
	setting := r.UILanguage()
	answers := onboard.Answers{Lang: i18n.Resolve(setting, os.Getenv), LanguageSetting: setting}
	_, err := onboard.RunWithKeys(ctx, os.Stdin, r.Out, setupPath(), answers, time.Now(), KeyModeFor(os.Stdin))
	return err
}

// setupPath is the file the setup wizard writes and ReloadConfig reads: the motita home's
// configuration, or the working directory's when there is no home.
// saveUILanguage writes ui.language into the configuration file. It is a variable so a test can
// stand in for the file.
var saveUILanguage = config.SetUILanguage

// UILanguage is the ui.language setting: the one /language saved in this process, or the one the
// configuration was loaded with.
func (r *AppRunner) UILanguage() string {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	if r.uiLanguage != "" {
		return r.uiLanguage
	}
	return r.Cfg.UI.Language
}

// SetUILanguage saves ui.language in the configuration file the setup writes, so the next start
// and every other interface reading that file use it too.
func (r *AppRunner) SetUILanguage(lang string) error {
	if err := saveUILanguage(setupPath(), lang); err != nil {
		return err
	}
	r.cfgMu.Lock()
	r.uiLanguage = lang
	r.cfgMu.Unlock()
	return nil
}

func setupPath() string {
	if path := config.File(); path != "" {
		return path
	}
	return "./motita.yaml"
}

// ReloadConfig re-reads the configuration the setup wizard writes and applies what the wizard
// decides - the provider, its endpoint and key, the model, and the check - to the turns that follow.
//
// It exists because the wizard used to write the file and stop there: /config said
// "configuration written" while every following turn still ran with the provider and the key the
// program had started with, and the new setup only took effect after a restart nobody mentioned.
//
// A setup with no key yet is still applied - the interface then says the key is missing, which is
// the truth - but a file that does not load is an error, and nothing is changed.
func (r *AppRunner) ReloadConfig(context.Context) error {
	path := setupPath()
	cfg, err := config.Load(path)
	if err != nil {
		if cfg, err = config.LoadWithoutKey(path); err != nil {
			return err
		}
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.Cfg.LLM = cfg.LLM
	r.Cfg.Anchor = cfg.Anchor
	r.Cfg.Models = cfg.Models
	r.dropEngines()
	// The endpoints remembered for a provider switch belong to the setup that was just replaced.
	r.endpoints = nil
	return nil
}

// RunModels builds the report of the active setup and the catalogue the provider
// publishes, and returns it as text. It is the caller that decides where the
// report goes: the chat view keeps it in its own scrollback, which is what lets
// it be rendered inside the panel instead of being written over the frame.
//
// It is the answer to "is what I configured actually reachable?", which a user
// cannot check from the menu otherwise. The key is reported as present/absent and
// never printed, so the screen can be shared safely.
func (r *AppRunner) RunModels(ctx context.Context) (string, error) {
	cfg := r.Config()
	var b strings.Builder
	fmt.Fprintf(&b, "provider : %s\n", cfg.LLM.Provider)
	fmt.Fprintf(&b, "model    : %s\n", cfg.LLM.Model)
	if strings.EqualFold(cfg.LLM.Provider, "claude-code") {
		// No endpoint and no key: the claude CLI is the catalogue, and its login is the account.
		r.claudeCodeModels(ctx, &b, cfg.LLM.Model)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "base URL : %s\n", cfg.LLM.BaseURL)
	switch {
	case cfg.LLM.APIKey != "":
		fmt.Fprintf(&b, "api key  : present\n")
	case config.HasLogin(cfg.LLM.Provider):
		fmt.Fprintf(&b, "api key  : none needed (stored login)\n")
	case !config.LLMNeedsKey(cfg.LLM):
		fmt.Fprintf(&b, "api key  : none needed\n")
	default:
		fmt.Fprintf(&b, "api key  : MISSING (set %s)\n", config.ProviderKeyVariable(cfg.LLM.Provider))
	}

	base := cfg.LLM.BaseURL
	if base == "" {
		base = onboard.DefaultBaseURL(cfg.LLM.Provider)
	}
	fmt.Fprintf(&b, "\nmodels published by %s:\n", base)

	var models []string
	var err error
	if r.listModels != nil {
		models, err = r.listModels(ctx, base, cfg.LLM.APIKey)
	} else {
		// The provider's own client, so the listing is authenticated the way the requests
		// are: a header key for Anthropic and Gemini, a stored login for Copilot or Qwen.
		var client *llm.Client
		if client, err = llm.New(cfg.LLM, r.Log); err == nil {
			models, err = client.ListModels(ctx)
		}
	}
	if err != nil {
		// A listing failure must not look like a broken agent: say what failed
		// and still show the model the configuration will use.
		fmt.Fprintf(&b, "  could not read the catalogue: %v\n", err)
		fmt.Fprintf(&b, "  the configured model %q will still be used.\n", cfg.LLM.Model)
		// The error is reported in the text, not as a Go error: the report was
		// produced, and the caller has something useful to show.
		return b.String(), nil
	}
	for _, m := range models {
		mark := "  "
		if m == cfg.LLM.Model {
			mark = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", mark, m)
	}
	fmt.Fprintf(&b, "\n  (* is the model this configuration uses)\n")
	return b.String(), nil
}

// claudeCodeModels adds the plan and the models the logged-in claude account offers, as claude's
// own model picker lists them.
func (r *AppRunner) claudeCodeModels(ctx context.Context, b *strings.Builder, current string) {
	cat, err := r.claudeModels(ctx)
	if err != nil {
		fmt.Fprintf(b, "\n  could not read the account's models: %v\n", err)
		fmt.Fprintf(b, "  the configured model %q will still be used.\n", current)
		return
	}
	fmt.Fprintf(b, "plan     : %s\n", cat.Plan)
	b.WriteString("\nmodels on this account (/models <id> picks one for this session):\n")
	for _, m := range cat.Models {
		mark := "  "
		if m.Value == current {
			mark = "* "
		}
		fmt.Fprintf(b, "  %s%-22s %s", mark, m.Value, m.DisplayName)
		if m.Description != "" {
			fmt.Fprintf(b, " - %s", m.Description)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "\n  (* is the model this configuration uses)\n")
}
