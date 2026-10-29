package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// TTS is the gateway's voice: the browser interface sends an answer's text and plays the audio
// that comes back, the same voice on every device, instead of the device's own.
//
// It is OFF by default, because turning it on sends the agent's answers to a speech service. The
// endpoint speaks the OpenAI speech API (POST {base_url}/audio/speech), which OpenAI and local
// servers such as Kokoro-FastAPI or openedai-speech answer, so a private voice is a base_url away.
type TTS struct {
	Enabled bool `yaml:"enabled"`
	// BaseURL is the speech endpoint's base. Empty means the transcription endpoint (voice.base_url,
	// resolved the same way: the language model's own when it speaks the OpenAI API).
	BaseURL string `yaml:"base_url"`
	// APIKey is the key for BaseURL. Empty means the transcription key when the endpoint is the
	// transcription one, and no key otherwise (a local server needs none).
	APIKey string `yaml:"api_key"`
	// API is the protocol of the endpoint: "openai" (POST {base_url}/audio/speech) or
	// "dashscope" (Qwen3-TTS on Alibaba Model Studio). Empty means dashscope when base_url names a
	// DashScope host, and openai otherwise.
	API string `yaml:"api"`
	// Model is the speech model: gpt-4o-mini-tts or tts-1 on OpenAI, kokoro on Kokoro-FastAPI.
	Model string `yaml:"model"`
	// Voice is the voice the page uses unless the person picks another: alloy, nova, ...
	Voice string `yaml:"voice"`
	// Voices are the voices the settings offer. Empty means Voice alone.
	Voices []string `yaml:"voices"`
	// Format is the audio format asked for: mp3 (the default), opus, aac, flac or wav.
	Format string `yaml:"format"`
	// MaxChars caps the text of one request. Zero means the built-in default.
	MaxChars int `yaml:"max_chars"`
}

// Defaults of the tts block.
const (
	defaultTTSModel    = "gpt-4o-mini-tts"
	defaultTTSVoice    = "alloy"
	defaultTTSFormat   = "mp3"
	defaultTTSMaxChars = 4000
)

// TTSFormats are the formats the OpenAI speech API answers that every browser can play or that a
// local server is likely to offer.
var TTSFormats = []string{"mp3", "opus", "aac", "flac", "wav"}

// defaultTTS is the tts block of the default configuration.
func defaultTTS() TTS {
	return TTS{Model: defaultTTSModel, Voice: defaultTTSVoice, Format: defaultTTSFormat, MaxChars: defaultTTSMaxChars}
}

// ttsInherits reports whether the speech endpoint is the transcription one.
func (c Config) ttsInherits() bool {
	return strings.TrimSpace(c.Voice.TTS.BaseURL) == ""
}

// TTSBaseURL is the speech endpoint's base, resolved: the tts block's own, or the transcription
// endpoint's. Empty means there is none.
func (c Config) TTSBaseURL() string {
	if c.ttsInherits() {
		return c.VoiceBaseURL()
	}
	return strings.TrimRight(strings.TrimSpace(c.Voice.TTS.BaseURL), "/")
}

// TTSAPIKey is the key the speech endpoint is called with: the tts block's own, or the
// transcription key when the endpoint is the transcription one. A key is never sent to an endpoint
// it was not issued for.
func (c Config) TTSAPIKey() string {
	if key := strings.TrimSpace(c.Voice.TTS.APIKey); key != "" {
		return key
	}
	if c.ttsInherits() {
		return c.VoiceAPIKey()
	}
	return ""
}

// TTS protocols.
const (
	TTSAPIOpenAI    = "openai"
	TTSAPIDashScope = "dashscope"
)

// TTSAPI is the protocol the speech endpoint is called with, resolved.
func (c Config) TTSAPI() string {
	if api := c.Voice.TTS.API; api != "" {
		return api
	}
	if strings.Contains(strings.ToLower(c.TTSBaseURL()), "dashscope") {
		return TTSAPIDashScope
	}
	return TTSAPIOpenAI
}

// TTSMaxChars is the longest text one request may carry.
func (c Config) TTSMaxChars() int {
	if c.Voice.TTS.MaxChars == 0 {
		return defaultTTSMaxChars
	}
	return c.Voice.TTS.MaxChars
}

// TTSVoices are the voices the settings offer: the listed ones, with the default first.
func (c Config) TTSVoices() []string {
	out := []string{c.Voice.TTS.Voice}
	for _, v := range c.Voice.TTS.Voices {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// validateTTS checks the tts block. A disabled block is not checked.
func (c *Config) validateTTS() error {
	t := &c.Voice.TTS
	t.Model, t.Voice = strings.TrimSpace(t.Model), strings.TrimSpace(t.Voice)
	t.Format = strings.ToLower(strings.TrimSpace(t.Format))
	t.API = strings.ToLower(strings.TrimSpace(t.API))
	if !t.Enabled {
		return nil
	}
	if t.MaxChars < 0 {
		return fmt.Errorf("voice.tts.max_chars is %d: it cannot be negative (0 means the built-in default)", t.MaxChars)
	}
	if t.Model == "" {
		return errors.New("voice.tts.model is empty: name the speech model (gpt-4o-mini-tts on OpenAI)")
	}
	if t.Voice == "" {
		return errors.New("voice.tts.voice is empty: name the voice (alloy on OpenAI)")
	}
	if t.API != "" && t.API != TTSAPIOpenAI && t.API != TTSAPIDashScope {
		return fmt.Errorf("voice.tts.api %q is not %s or %s", t.API, TTSAPIOpenAI, TTSAPIDashScope)
	}
	if !slices.Contains(TTSFormats, t.Format) {
		return fmt.Errorf("voice.tts.format %q is not one of %s", t.Format, strings.Join(TTSFormats, ", "))
	}
	base := c.TTSBaseURL()
	if base == "" {
		return fmt.Errorf("voice.tts.base_url is empty and the %s provider has no speech endpoint: "+
			"name one (an OpenAI-compatible /audio/speech)", c.LLM.Provider)
	}
	return checkKeyedEndpoint("voice.tts.base_url", base, c.TTSAPIKey())
}
