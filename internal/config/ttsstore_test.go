package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func clearTTSEnv(t *testing.T) {
	t.Helper()
	names := []string{"MOTITA_VOICE_TTS_ENABLED", "MOTITA_VOICE_TTS_API_KEY", "MOTITA_VOICE_TTS_API", "DASHSCOPE_API_KEY"}
	for _, name := range names {
		t.Setenv(name, "")
	}
}

// The edit stays inside voice.tts: its comments, its nested lists and the rest of the voice block
// and of the file are untouched.
func TestSetTTSTextEditsOnlyTheTTSBlock(t *testing.T) {
	in := "voice:\n  enabled: true\n  tts: # speech\n    enabled: false # off\n    api_key: old\n" +
		"    voices:\n      - a\n  model: whisper-1\nui:\n  language: es\n"
	got, err := setTTSText(in, TTSChange{
		Enabled: true, API: strp("dashscope"), Model: strp(" qwen3-tts-flash "), Voice: strp("Cherry"),
		BaseURL: strp(""), APIKey: strp("k"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  enabled: true\n", "  tts: # speech\n", "    enabled: true # off\n",
		"    api: \"dashscope\"\n", "    model: \"qwen3-tts-flash\"\n", "    voice: \"Cherry\"\n",
		"    base_url: \"\"\n", "    api_key: \"\"\n", "      - a\n", "  model: whisper-1\n", "ui:\n  language: es\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A missing tts block (and a missing voice block) is added in reading order.
func TestSetTTSTextAddsWhatIsMissing(t *testing.T) {
	want := "voice:\n  tts:\n    enabled: true\n    api: \"openai\"\n"
	for _, in := range []string{"llm:\n  provider: openai\n", ""} {
		got, err := setTTSText(in, TTSChange{Enabled: true, API: strp("openai")})
		if err != nil || !strings.HasSuffix(got, want) {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	got, err := setTTSText("voice:\n  enabled: true\n", TTSChange{Enabled: false})
	if err != nil || got != "voice:\n  tts:\n    enabled: false\n  enabled: true\n" {
		t.Errorf("tts in an existing voice block: %q, %v", got, err)
	}
	got, err = setTTSText("voice:\nui:\n  language: es\n", TTSChange{Enabled: false})
	if err != nil || !strings.HasPrefix(got, "voice:\n  tts:\n    enabled: false\nui:") {
		t.Errorf("an empty voice block: %q, %v", got, err)
	}
	got, err = setTTSText("voice:\n    enabled: true\n", TTSChange{Enabled: false})
	if err != nil || !strings.Contains(got, "    tts:\n      enabled: false\n") {
		t.Errorf("the block's own indent is kept: %q, %v", got, err)
	}
}

func TestSetTTSTextRefusesInlineBlocks(t *testing.T) {
	for _, in := range []string{"voice: {enabled: true}\n", "voice:\n  tts: {enabled: true}\n"} {
		if _, err := setTTSText(in, TTSChange{}); err == nil || !strings.Contains(err.Error(), "inline value") {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func TestSetTTSSavesAndLoads(t *testing.T) {
	clearTTSEnv(t)
	path := voiceFile(t, "llm:\n  provider: openai\n")
	cfg, err := SetTTS(path, TTSChange{Enabled: true, API: strp("dashscope"), APIKey: strp(" dk ")})
	if err != nil || !cfg.Voice.TTS.Enabled || cfg.Voice.TTS.API != TTSAPIDashScope || cfg.TTSAPIKey() != "dk" {
		t.Fatalf("cfg = %+v, %v", cfg.Voice.TTS, err)
	}
	// The key is kept beside the file, never in it.
	if body, _ := os.ReadFile(path); strings.Contains(string(body), "dk") {
		t.Errorf("the key reached the YAML:\n%s", body)
	}
	cfg, err = SetTTS(path, TTSChange{Enabled: false})
	if err != nil || cfg.Voice.TTS.Enabled || cfg.TTSAPIKey() != "dk" {
		t.Fatalf("turning off keeps the key: %+v, %v", cfg.Voice.TTS, err)
	}
}

func TestSetTTSUndoesAnInvalidChange(t *testing.T) {
	clearTTSEnv(t)
	const body = "llm:\n  provider: openai\n"
	path := voiceFile(t, body)
	_, err := SetTTS(path, TTSChange{Enabled: true, API: strp("polly")})
	if err == nil || !strings.Contains(err.Error(), "voice.tts.api") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Fatalf("restored = %q", got)
	}
	// A key for a plain-http remote endpoint is refused before anything is kept.
	_, err = SetTTS(path, TTSChange{Enabled: true, BaseURL: strp("http://tts.example.com/v1"), APIKey: strp("k")})
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("restored = %q", got)
	}
	if key := readCredentials(CredentialsPath(path))[TTSKeyVariable]; key != "" {
		t.Errorf("the key must not stay stored: %q", key)
	}
}

func TestSetTTSReportsAnEnvironmentOverride(t *testing.T) {
	clearTTSEnv(t)
	t.Setenv("MOTITA_VOICE_TTS_ENABLED", "false")
	_, err := SetTTS(voiceFile(t, "llm:\n  provider: openai\n"), TTSChange{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "MOTITA_VOICE_TTS_ENABLED") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetTTSFailures(t *testing.T) {
	clearTTSEnv(t)
	if _, err := SetTTS(t.TempDir(), TTSChange{}); err == nil {
		t.Error("a directory is not a configuration")
	}
	if _, err := SetTTS("/nonexistent/motita.yaml", TTSChange{}); err == nil {
		t.Error("a missing file is an error")
	}
	if _, err := SetTTS(voiceFile(t, "voice: {enabled: true}\n"), TTSChange{}); err == nil {
		t.Error("an inline block is refused")
	}

	old := setTTSCredential
	setTTSCredential = func(string, string, string) error { return errors.New("read-only") }
	t.Cleanup(func() { setTTSCredential = old })
	file := voiceFile(t, "llm:\n  provider: openai\n")
	if _, err := SetTTS(file, TTSChange{APIKey: strp("k")}); err == nil {
		t.Error("a key that cannot be stored is an error")
	}

	// A key that cannot be put back after a refused change says so.
	n := 0
	setTTSCredential = func(string, string, string) error {
		if n++; n > 1 {
			return errors.New("read-only")
		}
		return nil
	}
	bad := TTSChange{Enabled: true, API: strp("polly"), APIKey: strp("k")}
	_, err := SetTTS(voiceFile(t, "llm:\n  provider: openai\n"), bad)
	if err == nil || !strings.Contains(err.Error(), "key could not be restored") {
		t.Errorf("err = %v", err)
	}
	setTTSCredential = func(string, string, string) error { return nil }

	create := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = create })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	if _, err := SetTTS(voiceFile(t, "llm:\n  provider: openai\n"), TTSChange{}); err == nil {
		t.Error("a write that fails is an error")
	}
	calls := 0
	uiCreateTemp = func(dir, pattern string) (*os.File, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("disk full")
		}
		return create(dir, pattern)
	}
	_, err = SetTTS(voiceFile(t, "llm:\n  provider: openai\n"), TTSChange{Enabled: true, API: strp("polly")})
	if err == nil || !strings.Contains(err.Error(), "could not be restored") {
		t.Errorf("err = %v", err)
	}
}
