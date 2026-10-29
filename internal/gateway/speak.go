package gateway

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/voice"
)

// SPEECH OUTPUT for the browser interface: the gateway's voice.
//
// The page sends the text of what it is about to read, a sentence or a few at a time, and plays
// the audio that comes back. The same voice on every device, instead of whatever voices the
// device has. Nothing is kept: the text and the audio live for the length of one request.
// PROCESS-WIDE, like speech input.

// SpeechService says one text with one voice. *voice.Speaker is the production one.
type SpeechService interface {
	Speak(ctx context.Context, text, voice string) ([]byte, string, error)
}

// defaultSpeechMaxChars caps one text when the options name no cap.
const defaultSpeechMaxChars = 4000

// ttsView is the gateway's voice as GET /v1/voice answers it.
type ttsView struct {
	Enabled bool `json:"enabled"`
	// Voices are the voices the page may ask for, the default first.
	Voices   []string `json:"voices"`
	MaxChars int      `json:"max_chars"`
}

// speechSettings is the gateway's voice in force: the service (nil when off), the voices offered and
// the cap on one text.
type speechSettings struct {
	svc      SpeechService
	voices   []string
	maxChars int
}

// speechNow is the voice in force: the last change PUT /v1/voice/tts applied, or the options of the
// start.
func (s *Server) speechNow() speechSettings {
	s.voiceMu.RLock()
	live := s.speechLive
	s.voiceMu.RUnlock()
	if live != nil {
		return *live
	}
	return speechSettings{svc: s.opts.Speech, voices: s.opts.SpeechVoices, maxChars: s.opts.SpeechMaxChars}
}

// limit is the longest text one request may carry, in characters.
func (n speechSettings) limit() int {
	if n.maxChars > 0 {
		return n.maxChars
	}
	return defaultSpeechMaxChars
}

// ttsViewOf answers whether the gateway has a voice, and which.
func (s *Server) ttsViewOf() ttsView {
	now := s.speechNow()
	voices := now.voices
	if voices == nil || now.svc == nil {
		voices = []string{}
	}
	return ttsView{Enabled: now.svc != nil, Voices: voices, MaxChars: now.limit()}
}

// handleSpeak says the text in the request with the gateway's voice and answers the audio.
//
// The voice must be one the configuration offers (empty means the default): the page cannot make
// the gateway ask the speech service for anything the operator did not list.
func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	now := s.speechNow()
	if now.svc == nil {
		writeError(w, http.StatusNotImplemented, "the gateway has no voice: turn it on in the settings (Voice) "+
			"or set voice.tts.enabled in the configuration")
		return
	}
	var body struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "there is no text to say")
		return
	}
	if n, limit := utf8.RuneCountInString(text), now.limit(); n > limit {
		writeError(w, http.StatusRequestEntityTooLarge,
			"the text has "+strconv.Itoa(n)+" characters, more than voice.tts.max_chars ("+strconv.Itoa(limit)+")")
		return
	}
	voices := now.voices
	name := strings.TrimSpace(body.Voice)
	switch {
	case name == "" && len(voices) > 0:
		name = voices[0]
	case name != "" && !slices.Contains(voices, name):
		writeError(w, http.StatusBadRequest, "the voice "+strconv.Quote(name)+" is not one voice.tts offers")
		return
	default:
		// A voice voice.tts offers, or none when it lists none: said as asked.
	}
	audio, ctype, err := now.svc.Speak(r.Context(), text, name)
	if err != nil {
		s.warn("a text could not be said", "error", err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h := w.Header()
	h.Set(headerContentType, ctype)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(audio)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

// ttsSaved is the voice.tts block as the configuration file has it. The key is never answered: only
// whether there is one.
type ttsSaved struct {
	Enabled bool `json:"enabled"`
	// API is the protocol, "openai" or "dashscope". BaseURL is voice.tts.base_url as written;
	// Endpoint is where texts go, resolved, and empty when there is nowhere.
	API      string `json:"api"`
	BaseURL  string `json:"base_url"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Voice    string `json:"voice"`
	HasKey   bool   `json:"has_key"`
}

// savedTTS is the voice.tts block of a loaded configuration, as the page sees it.
func savedTTS(cfg config.Config) ttsSaved {
	t := cfg.Voice.TTS
	return ttsSaved{
		Enabled: t.Enabled, API: t.API, BaseURL: strings.TrimSpace(t.BaseURL), Endpoint: cfg.TTSBaseURL(),
		Model: t.Model, Voice: t.Voice, HasKey: cfg.TTSAPIKey() != "",
	}
}

// setTTS is config.SetTTS, as a variable so a test can make the write fail.
var setTTS = config.SetTTS

// handlePutTTS turns the gateway's voice on or off, and changes where texts go, from the settings.
//
// Turning it on, or pointing it somewhere else, sends the text of every answer read to a service,
// so it needs confirm in the request itself: the page asks the person first, and a client that
// skips the question is refused here. Turning it off needs nothing. The change is saved in the
// configuration file and, where the gateway can build a voice, applied at once.
func (s *Server) handlePutTTS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool    `json:"enabled"`
		Confirm bool    `json:"confirm"`
		API     *string `json:"api"`
		BaseURL *string `json:"base_url"`
		Model   *string `json:"model"`
		Voice   *string `json:"voice"`
		APIKey  *string `json:"api_key"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if s.opts.ConfigPath == "" {
		writeError(w, http.StatusConflict, "this gateway runs on the built-in defaults and has no configuration file "+
			"to save the voice in: run the setup (motita config) or start it with -config")
		return
	}
	if body.Enabled && !body.Confirm && s.ttsNeedsConfirm(body.API, body.BaseURL) {
		writeError(w, http.StatusBadRequest, "turning the voice on sends the text of every answer read to the speech "+
			"service: it needs confirm: true, sent after the person has been told where it goes")
		return
	}
	cfg, err := setTTS(s.opts.ConfigPath, config.TTSChange{
		Enabled: body.Enabled, API: body.API, BaseURL: body.BaseURL, Model: body.Model, Voice: body.Voice,
		APIKey: body.APIKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.opts.SpeechFrom != nil {
		live := &speechSettings{voices: cfg.TTSVoices(), maxChars: cfg.TTSMaxChars()}
		if cfg.Voice.TTS.Enabled {
			live.svc = s.opts.SpeechFrom(cfg)
		}
		s.voiceMu.Lock()
		s.speechLive = live
		s.voiceMu.Unlock()
	}
	s.info("the voice was changed from the settings", "enabled", cfg.Voice.TTS.Enabled,
		"api", cfg.Voice.TTS.API, "endpoint", cfg.TTSBaseURL())
	writeJSON(w, http.StatusOK, s.voiceViewOf())
}

// ttsNeedsConfirm reports whether turning the voice on with this api and base_url sends text
// somewhere the person has not agreed to yet: it is off now, or the protocol or the address changes.
func (s *Server) ttsNeedsConfirm(api, baseURL *string) bool {
	if s.speechNow().svc == nil {
		return true
	}
	cfg, err := loadConfig(s.opts.ConfigPath)
	if err != nil {
		return true
	}
	changed := func(next *string, now string) bool {
		return next != nil && strings.TrimSpace(*next) != strings.TrimSpace(now)
	}
	return changed(api, cfg.Voice.TTS.API) || changed(baseURL, cfg.Voice.TTS.BaseURL)
}

// detectSpeech is voice.DetectSpeech, as a variable so a test need not listen on the common ports.
var detectSpeech = voice.DetectSpeech

// handleDetectSpeech looks on the gateway's machine for servers that speak the OpenAI speech API and
// answers where they are. It runs only when the person asks (the settings' button): it probes the
// loopback with OPTIONS and GET /models, sends no text, and changes nothing. Using a server found
// is a separate step, PUT /v1/voice/tts, which needs confirm.
func (s *Server) handleDetectSpeech(w http.ResponseWriter, r *http.Request) {
	var extra []string
	var ports []int
	if s.opts.ConfigPath != "" {
		if cfg, err := loadConfig(s.opts.ConfigPath); err == nil {
			extra, ports = append(extra, cfg.TTSBaseURL()), cfg.Voice.TTS.DetectPorts
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": detectSpeech(r.Context(), ports, extra...)})
}
