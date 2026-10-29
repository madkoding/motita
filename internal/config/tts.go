package config

import (
	"errors"
	"fmt"
	"os"
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
	// API is the protocol of the endpoint: "openai" (the default) or "dashscope", Alibaba Cloud
	// Model Studio's native speech API, the hosted way to use Qwen3-TTS (it has no OpenAI-style
	// route). A self-hosted Qwen3-TTS server that answers /audio/speech is "openai".
	API string `yaml:"api"`
	// BaseURL is the speech endpoint's base. Empty means the transcription endpoint (voice.base_url,
	// resolved the same way: the language model's own when it speaks the OpenAI API), or DashScope's
	// international endpoint when API is "dashscope".
	BaseURL string `yaml:"base_url"`
	// APIKey is the key for BaseURL. Empty means the transcription key when the endpoint is the
	// transcription one, and no key otherwise (a local server needs none).
	APIKey string `yaml:"api_key"`
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
	// DetectPorts are extra loopback ports the settings and the doctor look at for a speech server,
	// besides the usual ones. Empty means the usual ones alone.
	DetectPorts []int `yaml:"detect_ports"`
}

// maxDetectPorts caps voice.tts.detect_ports: each port is probed twice, on every look.
const maxDetectPorts = 16

// Defaults of the tts block.
const (
	defaultTTSModel    = "gpt-4o-mini-tts"
	defaultTTSVoice    = "alloy"
	defaultTTSFormat   = "mp3"
	defaultTTSMaxChars = 4000

	// TTSAPIOpenAI and TTSAPIDashScope are the protocols voice.tts.api names.
	TTSAPIOpenAI    = "openai"
	TTSAPIDashScope = "dashscope"

	// DashScope's Qwen3-TTS: the international endpoint (the key of the Beijing region is another
	// one, and its base_url is named in the file), its model and its default voice. A request
	// takes 600 characters at most: the speaker splits longer texts itself.
	dashScopeTTSBaseURL = "https://dashscope-intl.aliyuncs.com/api/v1"
	dashScopeTTSModel   = "qwen3-tts-flash"
	dashScopeTTSVoice   = "Cherry"
)

// dashScopeTTSVoices are the Qwen3-TTS-Flash voices (Model Studio's voice list) the settings offer
// when voice.tts.voices names none: the system voices that speak every language, and the dialects.
var dashScopeTTSVoices = []string{
	"Cherry", "Serena", "Ethan", "Chelsie", "Momo", "Vivian", "Moon", "Maia", "Kai", "Nofish", "Bella",
	"Jennifer", "Ryan", "Katerina", "Aiden", "Mia", "Vincent", "Neil", "Elias", "Arthur", "Nini",
	"Sohee", "Alek", "Andre", "Dylan", "Sunny", "Rocky",
}

// TTSFormats are the formats the OpenAI speech API answers that every browser can play or that a
// local server is likely to offer.
var TTSFormats = []string{"mp3", "opus", "aac", "flac", "wav"}

// defaultTTS is the tts block of the default configuration.
func defaultTTS() TTS {
	return TTS{Model: defaultTTSModel, Voice: defaultTTSVoice, Format: defaultTTSFormat, MaxChars: defaultTTSMaxChars}
}

// ttsInherits reports whether the speech endpoint is the transcription one.
func (c Config) ttsInherits() bool {
	return c.Voice.TTS.API != TTSAPIDashScope && strings.TrimSpace(c.Voice.TTS.BaseURL) == ""
}

// TTSBaseURL is the speech endpoint's base, resolved: the tts block's own, or the transcription
// endpoint's. Empty means there is none.
func (c Config) TTSBaseURL() string {
	if c.ttsInherits() {
		return c.VoiceBaseURL()
	}
	if c.Voice.TTS.API == TTSAPIDashScope && strings.TrimSpace(c.Voice.TTS.BaseURL) == "" {
		return dashScopeTTSBaseURL
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
	if c.Voice.TTS.API == TTSAPIDashScope {
		return strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
	}
	if c.ttsInherits() {
		return c.VoiceAPIKey()
	}
	return ""
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
	listed := c.Voice.TTS.Voices
	if len(listed) == 0 && c.Voice.TTS.API == TTSAPIDashScope {
		listed = dashScopeTTSVoices
	}
	for _, v := range listed {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// normalize trims the tts block and fills what only the API's name implies.
func (t *TTS) normalize() {
	t.Model, t.Voice = strings.TrimSpace(t.Model), strings.TrimSpace(t.Voice)
	t.Format = strings.ToLower(strings.TrimSpace(t.Format))
	t.API = strings.ToLower(strings.TrimSpace(t.API))
	if t.API == "" {
		t.API = TTSAPIOpenAI
	}
	if t.API != TTSAPIDashScope {
		return
	}
	// Only the API was named: the OpenAI defaults of the model and voice mean nothing here.
	if t.Model == defaultTTSModel {
		t.Model = dashScopeTTSModel
	}
	if t.Voice == defaultTTSVoice {
		t.Voice = dashScopeTTSVoice
	}
}

// validateAPI checks the protocol.
func (t TTS) validateAPI() error {
	if t.API != TTSAPIOpenAI && t.API != TTSAPIDashScope {
		return fmt.Errorf("voice.tts.api %q is not one of %s, %s", t.API, TTSAPIOpenAI, TTSAPIDashScope)
	}
	return nil
}

// validateDetectPorts checks the ports looked at for a local speech server, enabled or not.
func (t TTS) validateDetectPorts() error {
	if len(t.DetectPorts) > maxDetectPorts {
		return fmt.Errorf("voice.tts.detect_ports lists %d ports: at most %d", len(t.DetectPorts), maxDetectPorts)
	}
	for _, p := range t.DetectPorts {
		if p < 1 || p > 65535 {
			return fmt.Errorf("voice.tts.detect_ports has %d: a port is 1 to 65535", p)
		}
	}
	return nil
}

// validateTTS checks the tts block. A disabled block is not checked.
func (c *Config) validateTTS() error {
	t := &c.Voice.TTS
	t.normalize()
	if err := t.validateDetectPorts(); err != nil {
		return err
	}
	if !t.Enabled {
		return nil
	}
	if err := t.validateAPI(); err != nil {
		return err
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
	if !slices.Contains(TTSFormats, t.Format) {
		return fmt.Errorf("voice.tts.format %q is not one of %s", t.Format, strings.Join(TTSFormats, ", "))
	}
	base := c.TTSBaseURL()
	if t.API == TTSAPIDashScope && c.TTSAPIKey() == "" {
		return errors.New("voice.tts.api_key is empty: DashScope needs a key (or DASHSCOPE_API_KEY)")
	}
	if base == "" {
		return fmt.Errorf("voice.tts.base_url is empty and the %s provider has no speech endpoint: "+
			"name one (an OpenAI-compatible /audio/speech)", c.LLM.Provider)
	}
	return checkKeyedEndpoint("voice.tts.base_url", base, c.TTSAPIKey())
}
