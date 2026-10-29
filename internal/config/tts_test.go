package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTTSIsOffByDefaultAndNotChecked(t *testing.T) {
	c := Default()
	tts := c.Voice.TTS
	defaults := tts.Model == "gpt-4o-mini-tts" && tts.Voice == "alloy" && tts.Format == "mp3"
	if tts.Enabled || !defaults || c.TTSMaxChars() != 4000 {
		t.Fatalf("default tts = %+v", tts)
	}
	c.Voice.TTS.MaxChars = -1
	if err := c.ValidateWithoutKey(); err != nil {
		t.Errorf("a disabled block must not be checked: %v", err)
	}
}

// The speech endpoint's own address wins; otherwise it is the transcription endpoint, with the
// transcription key, and an address of its own never gets a key it was not given.
func TestTTSEndpointResolution(t *testing.T) {
	c := Default()
	c.LLM.Provider, c.LLM.APIKey = "openai", "llm-key"
	if c.TTSBaseURL() != "https://api.openai.com/v1" || c.TTSAPIKey() != "llm-key" {
		t.Errorf("inherited = %q %q", c.TTSBaseURL(), c.TTSAPIKey())
	}
	c.Voice.BaseURL, c.Voice.APIKey = "https://api.groq.com/openai/v1", "voice-key"
	if c.TTSBaseURL() != "https://api.groq.com/openai/v1" || c.TTSAPIKey() != "voice-key" {
		t.Errorf("the transcription endpoint is inherited: %q %q", c.TTSBaseURL(), c.TTSAPIKey())
	}
	c.Voice.TTS.BaseURL = " http://127.0.0.1:8880/v1/ "
	if c.TTSBaseURL() != "http://127.0.0.1:8880/v1" || c.TTSAPIKey() != "" {
		t.Errorf("an endpoint of its own gets no inherited key: %q %q", c.TTSBaseURL(), c.TTSAPIKey())
	}
	c.Voice.TTS.APIKey = " own "
	if c.TTSAPIKey() != "own" {
		t.Errorf("own key = %q", c.TTSAPIKey())
	}
	c.Voice.TTS.MaxChars = 0
	if c.TTSMaxChars() != 4000 {
		t.Errorf("zero means the default: %d", c.TTSMaxChars())
	}
	c.Voice.TTS.MaxChars = 10
	if c.TTSMaxChars() != 10 {
		t.Errorf("max = %d", c.TTSMaxChars())
	}
	c.Voice.TTS.Voices = []string{"nova", " ", "alloy", "echo"}
	if got := c.TTSVoices(); !slices.Equal(got, []string{"alloy", "nova", "echo"}) {
		t.Errorf("voices = %v", got)
	}
}

func TestValidateTTS(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"ok", func(*Config) {}, ""},
		{"format case", func(c *Config) { c.Voice.TTS.Format = " WAV " }, ""},
		{"negative", func(c *Config) { c.Voice.TTS.MaxChars = -1 }, "voice.tts.max_chars"},
		{"model", func(c *Config) { c.Voice.TTS.Model = " " }, "voice.tts.model"},
		{"voice", func(c *Config) { c.Voice.TTS.Voice = "" }, "voice.tts.voice"},
		{"format", func(c *Config) { c.Voice.TTS.Format = "ogg" }, "voice.tts.format"},
		{"no endpoint", func(c *Config) { c.LLM.Provider = "anthropic" }, "voice.tts.base_url is empty"},
		{"scheme", func(c *Config) { c.Voice.TTS.BaseURL = "ftp://x" }, "not an http(s) URL"},
		{"clear text remote with key", func(c *Config) {
			c.Voice.TTS.BaseURL, c.Voice.TTS.APIKey = "http://tts.example.com/v1", "tk"
		}, "voice.tts.base_url \"http://tts.example.com/v1\" is plain http"},
		{"clear text remote without key", func(c *Config) { c.Voice.TTS.BaseURL = "http://tts.example.com/v1" }, ""},
		{"api unknown", func(c *Config) { c.Voice.TTS.API = "polly" }, "voice.tts.api"},
		{"dashscope", func(c *Config) { c.Voice.TTS.API, c.Voice.TTS.APIKey = " DashScope ", "dk" }, ""},
		{"dashscope no key", func(c *Config) {
			c.Voice.TTS.API = "dashscope"
			t.Setenv("DASHSCOPE_API_KEY", "")
		}, "voice.tts.api_key is empty"},
		{"dashscope key from env", func(c *Config) {
			c.Voice.TTS.API = "dashscope"
			t.Setenv("DASHSCOPE_API_KEY", " ek ")
		}, ""},
		{"clear text local with key", func(c *Config) {
			c.Voice.TTS.BaseURL, c.Voice.TTS.APIKey = "http://localhost:8880/v1", "tk"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.LLM.Provider, c.LLM.APIKey = "openai", "k"
			c.Voice.TTS.Enabled = true
			tc.edit(&c)
			err := c.ValidateWithoutKey()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// The block is read from the file and from the variables the reference documents.
func TestTTSComesFromTheFileAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	body := "llm:\n  provider: openai\n  api_key: k\nvoice:\n  tts:\n    enabled: true\n" +
		"    voice: nova\n    voices: [nova, shimmer]\n    format: opus\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatal(err)
	}
	tts := c.Voice.TTS
	fromFile := tts.Voice == "nova" && tts.Format == "opus" && len(tts.Voices) == 2
	if !tts.Enabled || !fromFile || tts.Model != "gpt-4o-mini-tts" {
		t.Errorf("from the file: %+v", tts)
	}

	t.Setenv("MOTITA_VOICE_TTS_ENABLED", "true")
	t.Setenv("MOTITA_VOICE_TTS_BASE_URL", "http://127.0.0.1:8880/v1")
	t.Setenv("MOTITA_VOICE_TTS_API_KEY", "x")
	t.Setenv("MOTITA_VOICE_TTS_MODEL", "kokoro")
	t.Setenv("MOTITA_VOICE_TTS_VOICE", "ef_dora")
	t.Setenv("MOTITA_VOICE_TTS_FORMAT", "wav")
	t.Setenv("MOTITA_VOICE_TTS_MAX_CHARS", "900")
	c = Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	want := TTS{Enabled: true, BaseURL: "http://127.0.0.1:8880/v1", APIKey: "x", Model: "kokoro",
		Voice: "ef_dora", Format: "wav", MaxChars: 900}
	if !ttsEqual(c.Voice.TTS, want) {
		t.Errorf("from the environment: %+v", c.Voice.TTS)
	}
}

func ttsEqual(a, b TTS) bool {
	sameEndpoint := a.Enabled == b.Enabled && a.BaseURL == b.BaseURL && a.APIKey == b.APIKey
	sameSpeech := a.Model == b.Model && a.Voice == b.Voice && a.Format == b.Format
	return sameEndpoint && sameSpeech && a.MaxChars == b.MaxChars && slices.Equal(a.Voices, b.Voices)
}

// DashScope is Qwen3-TTS's hosted API: its own endpoint, model, voice and cap, never the
// transcription endpoint or its key.
func TestTTSDashScopeDefaults(t *testing.T) {
	c := Default()
	c.LLM.Provider, c.LLM.APIKey = "openai", "llm-key"
	c.Voice.TTS.Enabled, c.Voice.TTS.API, c.Voice.TTS.APIKey = true, "dashscope", "dk"
	if err := c.ValidateWithoutKey(); err != nil {
		t.Fatal(err)
	}
	tts := c.Voice.TTS
	if tts.Model != "qwen3-tts-flash" || tts.Voice != "Cherry" {
		t.Errorf("defaults = %+v, max %d", tts, c.TTSMaxChars())
	}
	if v := c.TTSVoices(); v[0] != "Cherry" || len(v) < 20 {
		t.Errorf("voices = %v", v)
	}
	c.Voice.TTS.Voices = []string{"Ethan"}
	if v := c.TTSVoices(); len(v) != 2 {
		t.Errorf("listed voices win: %v", v)
	}
	if c.TTSBaseURL() != "https://dashscope-intl.aliyuncs.com/api/v1" || c.TTSAPIKey() != "dk" {
		t.Errorf("endpoint = %q key %q", c.TTSBaseURL(), c.TTSAPIKey())
	}
	c.Voice.TTS.BaseURL = "https://dashscope.aliyuncs.com/api/v1/"
	if c.TTSBaseURL() != "https://dashscope.aliyuncs.com/api/v1" {
		t.Errorf("own base = %q", c.TTSBaseURL())
	}
}
