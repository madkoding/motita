package config

import (
	"strings"
	"testing"
)

func TestVoiceIsOffByDefaultAndNotChecked(t *testing.T) {
	c := Default()
	if c.Voice.Enabled || c.Voice.Model != "whisper-1" || c.VoiceMaxAudioBytes() != 10*1024*1024 {
		t.Fatalf("default voice = %+v", c.Voice)
	}
	// Off means nothing in the block is checked, the way a disabled gateway is not.
	c.Voice.MaxAudioKB = -1
	if err := c.ValidateWithoutKey(); err != nil {
		t.Errorf("a disabled block must not be checked: %v", err)
	}
}

// TestVoiceEndpointResolution: the voice block's own endpoint wins; otherwise an OpenAI provider
// lends its endpoint AND its key, and any other provider lends neither.
func TestVoiceEndpointResolution(t *testing.T) {
	c := Default()
	c.LLM.Provider, c.LLM.APIKey, c.LLM.BaseURL = "openai", "llm-key", ""
	if c.VoiceBaseURL() != "https://api.openai.com/v1" || c.VoiceAPIKey() != "llm-key" {
		t.Errorf("openai default = %q %q", c.VoiceBaseURL(), c.VoiceAPIKey())
	}
	c.LLM.BaseURL = "https://api.groq.com/openai/v1/"
	if c.VoiceBaseURL() != "https://api.groq.com/openai/v1" {
		t.Errorf("an OpenAI-compatible endpoint is inherited: %q", c.VoiceBaseURL())
	}
	c.Voice.BaseURL = " http://127.0.0.1:8080/v1/ "
	if c.VoiceBaseURL() != "http://127.0.0.1:8080/v1" || c.VoiceAPIKey() != "" {
		t.Errorf("an endpoint of its own gets no inherited key: %q %q", c.VoiceBaseURL(), c.VoiceAPIKey())
	}
	c.Voice.APIKey = " own "
	if c.VoiceAPIKey() != "own" {
		t.Errorf("own key = %q", c.VoiceAPIKey())
	}
	c = Default()
	c.LLM.Provider, c.LLM.APIKey = "anthropic", "a-key"
	if c.VoiceBaseURL() != "" || c.VoiceAPIKey() != "" {
		t.Errorf("anthropic lends nothing: %q %q", c.VoiceBaseURL(), c.VoiceAPIKey())
	}
	c.Voice.MaxAudioKB = 0
	if c.VoiceMaxAudioBytes() != 10*1024*1024 {
		t.Errorf("zero means the default: %d", c.VoiceMaxAudioBytes())
	}
	c.Voice.MaxAudioKB = 2
	if c.VoiceMaxAudioBytes() != 2048 {
		t.Errorf("max = %d", c.VoiceMaxAudioBytes())
	}
}

func TestValidateVoice(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"ok", func(*Config) {}, ""},
		{"negative", func(c *Config) { c.Voice.MaxAudioKB = -1 }, "voice.max_audio_kb"},
		{"model", func(c *Config) { c.Voice.Model = "  " }, "voice.model"},
		{"no endpoint", func(c *Config) {
			c.LLM.Provider = "anthropic"
			c.LLM.APIKey = "k"
		}, "voice.base_url is empty"},
		{"scheme", func(c *Config) { c.Voice.BaseURL = "ftp://x" }, "not an http(s) URL"},
		{"no host", func(c *Config) { c.Voice.BaseURL = "http://" }, "not an http(s) URL"},
		{"unparsable", func(c *Config) { c.Voice.BaseURL = "http://a b\x7f" }, "not an http(s) URL"},
		{"blank phrase", func(c *Config) { c.Voice.WakePhrases = []string{"hey motita", " "} }, "voice.wake_phrases[1]"},
		{"clear text remote with key", func(c *Config) {
			c.Voice.BaseURL, c.Voice.APIKey = "http://stt.example.com/v1", "vk"
		}, "voice.base_url \"http://stt.example.com/v1\" is plain http"},
		{"clear text remote without key", func(c *Config) { c.Voice.BaseURL = "http://stt.example.com/v1" }, ""},
		{"clear text on the LAN with key", func(c *Config) {
			c.Voice.BaseURL, c.Voice.APIKey = "http://192.168.1.5:8080/v1", "vk"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.LLM.Provider, c.LLM.APIKey = "openai", "k"
			c.Voice.Enabled = true
			c.Voice.WakePhrases = []string{"hey motita"}
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

func TestValidateGatewayTLS(t *testing.T) {
	cases := []struct {
		name              string
		listen, cert, key string
		want              string
	}{
		{"none", "", "", "", ""},
		{"all", "0.0.0.0:7478", "c.pem", "k.pem", ""},
		{"partial", "0.0.0.0:7478", "", "", "listen, cert and key together"},
		{"bad listen", "7478", "c.pem", "k.pem", "gateway.tls.listen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.LLM.Provider, c.LLM.APIKey = "openai", "k"
			c.Gateway.TLS = GatewayTLS{Listen: tc.listen, Cert: tc.cert, Key: tc.key}
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

// TestVoiceAndTLSComeFromTheEnvironment: the variables the reference documents reach the fields.
func TestVoiceAndTLSComeFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "true")
	t.Setenv("MOTITA_VOICE_BASE_URL", "http://127.0.0.1:8080/v1")
	t.Setenv("MOTITA_VOICE_MODEL", "whisper-large-v3-turbo")
	t.Setenv("MOTITA_VOICE_MAX_AUDIO_KB", "512")
	t.Setenv("MOTITA_GATEWAY_TLS_LISTEN", "0.0.0.0:7478")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	endpointOK := c.Voice.BaseURL == "http://127.0.0.1:8080/v1" && c.Voice.Model == "whisper-large-v3-turbo"
	voiceOK := c.Voice.Enabled && endpointOK
	if !voiceOK || c.Voice.MaxAudioKB != 512 || c.Gateway.TLS.Listen != "0.0.0.0:7478" {
		t.Errorf("voice = %+v tls = %+v", c.Voice, c.Gateway.TLS)
	}
}
