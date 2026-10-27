package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Voice is speech input for the browser interface: a recording is sent to a transcription
// endpoint and the text comes back to the composer.
//
// It is OFF by default, because turning it on sends audio somewhere: to the provider the
// language model already talks to, or to the endpoint named here. The endpoint speaks the
// OpenAI transcription API (POST {base_url}/audio/transcriptions), which OpenAI, Groq and a
// local whisper.cpp server all answer, so a private setup is a base_url away and needs no
// change to this binary.
type Voice struct {
	Enabled bool `yaml:"enabled"`
	// BaseURL is the transcription endpoint's base. Empty means the language model's own
	// endpoint, when the provider is OpenAI or OpenAI-compatible (llm.base_url, then
	// https://api.openai.com/v1); any other provider needs it written here.
	BaseURL string `yaml:"base_url"`
	// APIKey is the key for BaseURL. Empty means the language model's key when the endpoint is
	// the language model's own, and no key otherwise (a local server needs none).
	APIKey string `yaml:"api_key"`
	// Model is the transcription model: whisper-1 or gpt-4o-mini-transcribe on OpenAI,
	// whisper-large-v3-turbo on Groq, whatever a local server loaded.
	Model string `yaml:"model"`
	// Language is the ISO-639-1 code of the speech ("es", "en"). Empty lets the model detect it.
	Language string `yaml:"language"`
	// WakePhrases are the phrases that start a command in the interface's hands-free mode.
	// Several may be listed; the match ignores case, accents and punctuation.
	WakePhrases []string `yaml:"wake_phrases"`
	// MaxAudioKB caps one recording. Zero means the built-in default.
	MaxAudioKB int `yaml:"max_audio_kb"`
	// TTS is speech OUTPUT: the gateway's own voice for reading answers aloud. See tts.go.
	TTS TTS `yaml:"tts"`
}

// Defaults of the voice block.
const (
	defaultVoiceModel      = "whisper-1"
	defaultVoiceMaxAudioKB = 10 * 1024
	// openAIVoiceBaseURL is where an OpenAI provider with no base_url transcribes. It is the
	// same host llm uses by default; config cannot import llm, so the value is restated here.
	openAIVoiceBaseURL = "https://api.openai.com/v1"
)

// defaultVoice is the voice block of the default configuration.
func defaultVoice() Voice {
	return Voice{Model: defaultVoiceModel, MaxAudioKB: defaultVoiceMaxAudioKB, TTS: defaultTTS()}
}

// voiceInherits reports whether the voice endpoint is the language model's own.
func (c Config) voiceInherits() bool {
	return strings.TrimSpace(c.Voice.BaseURL) == "" && speaksOpenAIAPI(c.LLM.Provider)
}

// VoiceBaseURL is the transcription endpoint's base, resolved: the voice block's own, or the
// language model's when that one speaks the OpenAI API. Empty means there is none.
func (c Config) VoiceBaseURL() string {
	if base := strings.TrimSpace(c.Voice.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	if !c.voiceInherits() {
		return ""
	}
	if base := strings.TrimSpace(c.LLM.BaseURL); base != "" {
		return strings.TrimRight(base, "/")
	}
	return openAIVoiceBaseURL
}

// VoiceAPIKey is the key the transcription endpoint is called with: the voice block's own, or
// the language model's when the endpoint is the language model's. A key is never sent to an
// endpoint it was not issued for.
func (c Config) VoiceAPIKey() string {
	if key := strings.TrimSpace(c.Voice.APIKey); key != "" {
		return key
	}
	if c.voiceInherits() {
		return c.LLM.APIKey
	}
	return ""
}

// VoiceMaxAudioBytes is the largest recording accepted, in bytes.
func (c Config) VoiceMaxAudioBytes() int64 {
	kb := c.Voice.MaxAudioKB
	if kb == 0 {
		kb = defaultVoiceMaxAudioKB
	}
	return int64(kb) * 1024
}

// validateVoice checks the voice block. A disabled block is not checked, like the gateway; its
// tts block has a switch of its own.
func (c *Config) validateVoice() error {
	c.Voice.Model = strings.TrimSpace(c.Voice.Model)
	if err := c.validateTTS(); err != nil {
		return err
	}
	if !c.Voice.Enabled {
		return nil
	}
	if c.Voice.MaxAudioKB < 0 {
		return fmt.Errorf("voice.max_audio_kb is %d: it cannot be negative (0 means the built-in default)",
			c.Voice.MaxAudioKB)
	}
	if c.Voice.Model == "" {
		return errors.New("voice.model is empty: name the transcription model (whisper-1 on OpenAI)")
	}
	base := c.VoiceBaseURL()
	if base == "" {
		return fmt.Errorf("voice.base_url is empty and the %s provider has no transcription endpoint: "+
			"name one (an OpenAI-compatible /audio/transcriptions)", c.LLM.Provider)
	}
	if err := checkKeyedEndpoint("voice.base_url", base, c.VoiceAPIKey()); err != nil {
		return err
	}
	for i, p := range c.Voice.WakePhrases {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("voice.wake_phrases[%d] is blank", i)
		}
	}
	return nil
}

// checkKeyedEndpoint checks an endpoint the key is sent to: it must be an http(s) URL, and a key
// may not travel in clear text to a host outside this machine and its network, the rule
// llm.base_url follows. A recording or a key read on the way is as much a leak as an LLM prompt.
func checkKeyedEndpoint(field, base, key string) error {
	if !httpURL(base) {
		return fmt.Errorf("%s %q is not an http(s) URL", field, base)
	}
	if key != "" && IsClearTextToRemote(base) {
		return fmt.Errorf("%s %q is plain http to a host outside this machine and its network: "+
			"the key would travel in clear text, use https", field, base)
	}
	return nil
}

// httpURL reports whether an address is an http(s) URL with a host.
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// GatewayTLS is a second, encrypted listener of the gateway.
//
// It is a SECOND socket rather than a switch on the first one, so that the local clients - the
// terminal interface, `gateway status`, the service file - keep speaking plain HTTP to loopback
// and never have to trust a certificate. The browser on another device uses this one; that is
// also what lets it use the microphone, which a browser only grants to https or localhost.
type GatewayTLS struct {
	// Listen is host:port of the encrypted listener. Empty means there is none.
	Listen string `yaml:"listen"`
	// Cert and Key are the PEM files (mkcert, tailscale cert, a CA). Relative paths are
	// resolved against the motita home, like the token file.
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// validateGatewayTLS checks the encrypted listener: all three fields or none.
func (c *Config) validateGatewayTLS() error {
	t := c.Gateway.TLS
	set := 0
	for _, v := range []string{t.Listen, t.Cert, t.Key} {
		if strings.TrimSpace(v) != "" {
			set++
		}
	}
	switch set {
	case 0:
		return nil
	case 3:
	default:
		return errors.New("gateway.tls needs listen, cert and key together")
	}
	if _, _, err := net.SplitHostPort(strings.TrimSpace(t.Listen)); err != nil {
		return fmt.Errorf("gateway.tls.listen %q is not host:port: %w", t.Listen, err)
	}
	return nil
}
