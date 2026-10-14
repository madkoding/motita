package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

// TestSetVoiceTextEditsOnlyTheVoiceBlock: the lines of the block move, a sequence of phrases is
// replaced whole, and the rest of the file is untouched.
func TestSetVoiceTextEditsOnlyTheVoiceBlock(t *testing.T) {
	in := "# top\nllm:\n  provider: openai\nvoice:\n  enabled: false # off\n  api_key: old\n" +
		"  wake_phrases:\n    - a\n    - b\nui:\n  language: es\n"
	got, err := setVoiceText(in, VoiceChange{
		Enabled: true, Model: strp(" whisper-1 "), APIKey: strp("k"), WakePhrases: []string{"oye", " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# top\n", "  enabled: true # off\n", "  model: \"whisper-1\"\n",
		"  api_key: \"\"\n", "  wake_phrases: [\"oye\"]\n", "ui:\n  language: es\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- a") {
		t.Errorf("the old phrases stay:\n%s", got)
	}
	if _, err := setVoiceText("voice: {enabled: true}\n", VoiceChange{}); err == nil {
		t.Error("an inline block cannot be edited")
	}
}

func voiceFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSetVoiceUndoesAnInvalidChange: a provider with no transcription endpoint needs base_url, and
// the file is restored when the change would break the next start.
func TestSetVoiceUndoesAnInvalidChange(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	t.Setenv("MOTITA_VOICE_API_KEY", "")
	const body = "llm:\n  provider: gemini\n"
	path := voiceFile(t, body)
	_, err := SetVoice(path, VoiceChange{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "voice.base_url") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Fatalf("restored = %q", got)
	}
	cfg, err := SetVoice(path, VoiceChange{Enabled: true, BaseURL: strp("http://127.0.0.1:8080/v1"), APIKey: strp(" k ")})
	if err != nil || !cfg.Voice.Enabled || cfg.VoiceAPIKey() != "k" || cfg.VoiceBaseURL() != "http://127.0.0.1:8080/v1" {
		t.Fatalf("cfg = %+v, %v", cfg.Voice, err)
	}
	if _, err := SetVoice("/nonexistent/motita.yaml", VoiceChange{}); err == nil {
		t.Error("a missing file is an error")
	}
}

func TestSetVoiceReportsAnEnvironmentOverride(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "false")
	path := voiceFile(t, "llm:\n  provider: openai\n")
	_, err := SetVoice(path, VoiceChange{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "MOTITA_VOICE_ENABLED") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetVoiceCredentialFailure(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	old := setVoiceCredential
	setVoiceCredential = func(string, string, string) error { return errors.New("read-only") }
	t.Cleanup(func() { setVoiceCredential = old })
	path := voiceFile(t, "llm:\n  provider: openai\n")
	if _, err := SetVoice(path, VoiceChange{Enabled: false, APIKey: strp("k")}); err == nil {
		t.Fatal("a key that cannot be stored is an error")
	}
}

// TestSetVoiceFileFailures: a file that cannot be read, edited or written is an error, and a
// change that cannot be undone says so.
func TestSetVoiceFileFailures(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	if _, err := SetVoice(t.TempDir(), VoiceChange{}); err == nil {
		t.Error("a directory is not a configuration")
	}
	if _, err := SetVoice(voiceFile(t, "voice: {enabled: true}\n"), VoiceChange{}); err == nil {
		t.Error("an inline block is refused")
	}
	cfg, err := SetVoice(voiceFile(t, "llm:\n  provider: openai\n"), VoiceChange{Enabled: true})
	if err != nil || !cfg.Voice.Enabled {
		t.Fatalf("no key = %+v, %v", cfg.Voice, err)
	}

	old := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = old })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	if _, err := SetVoice(voiceFile(t, "llm:\n  provider: openai\n"), VoiceChange{}); err == nil {
		t.Error("a write that fails is an error")
	}
	calls := 0
	uiCreateTemp = func(dir, pattern string) (*os.File, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("disk full")
		}
		return old(dir, pattern)
	}
	_, err = SetVoice(voiceFile(t, "llm:\n  provider: gemini\n"), VoiceChange{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "could not be restored") {
		t.Errorf("err = %v", err)
	}
}

func TestSetVoiceTextWritesANewBlockInReadingOrder(t *testing.T) {
	got, err := setVoiceText("llm:\n  provider: openai\n", VoiceChange{
		Enabled: true, BaseURL: strp("https://x/v1"), Model: strp("m"), APIKey: strp("k"), WakePhrases: []string{"a"},
	})
	want := "voice:\n  enabled: true\n  base_url: \"https://x/v1\"\n  model: \"m\"\n  api_key: \"\"\n" +
		"  wake_phrases: [\"a\"]\n"
	if err != nil || !strings.HasSuffix(got, want) {
		t.Fatalf("got %q, %v", got, err)
	}
}
