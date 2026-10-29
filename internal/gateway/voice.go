package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/voice"
)

// SPEECH INPUT for the browser interface.
//
// The browser records, this server forwards the recording to the configured transcription
// endpoint and answers the text. The audio is held in memory for the length of the request and
// never written anywhere: there is no file to clean up and nothing for a later request to find.
// PROCESS-WIDE, like the interface language: it is one setting of the one configuration.

// VoiceService turns one recording into text. *voice.Transcriber is the production one.
type VoiceService interface {
	Transcribe(ctx context.Context, audio []byte, contentType string) (string, error)
}

// defaultMaxAudioBytes caps a recording when the options name no cap.
const defaultMaxAudioBytes = 10 << 20

// voiceView is what GET and PUT /v1/voice answer: whether speech input is available, the phrases
// the hands-free mode listens for, and what the configuration file says, for the settings page.
type voiceView struct {
	Enabled       bool     `json:"enabled"`
	WakePhrases   []string `json:"wake_phrases"`
	MaxAudioBytes int64    `json:"max_audio_bytes"`
	// CanSave is false when there is no configuration file to save a change in.
	CanSave bool `json:"can_save"`
	// Saved is the voice block of the configuration file. Absent without a file, or when the
	// file cannot be read.
	Saved *voiceSaved `json:"saved,omitempty"`
	// RestartNeeded says the saved block differs from the one in force: this gateway cannot
	// apply a change without starting again.
	RestartNeeded bool `json:"restart_needed"`
	// TTS is the gateway's voice, for reading answers aloud. See speak.go.
	TTS ttsView `json:"tts"`
	// Local says the page can download voices to run itself. See voicemodels.go.
	Local bool `json:"local"`
}

// voiceSaved is the voice block as the configuration file has it. The key is never answered:
// only whether there is one.
type voiceSaved struct {
	Enabled bool `json:"enabled"`
	// BaseURL is voice.base_url as written; Endpoint is where recordings go, resolved, and empty
	// when there is nowhere (a provider with no transcription API and no base_url).
	BaseURL  string `json:"base_url"`
	Endpoint string `json:"endpoint"`
	// Inherits says the endpoint and the key are the language model's own.
	Inherits    bool     `json:"inherits"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	HasKey      bool     `json:"has_key"`
	WakePhrases []string `json:"wake_phrases"`
	// TTS is the voice.tts block, for reading answers aloud.
	TTS ttsSaved `json:"tts"`
}

// transcription is what POST /v1/voice/transcribe answers.
type transcription struct {
	Text string `json:"text"`
	// Wake is the phrase found at the start of Text, when the request asked for one and one
	// was there. Absent otherwise.
	Wake *voice.Wake `json:"wake,omitempty"`
}

// voiceSettings is speech input in force: the transcriber (nil when off), the wake phrases and the
// recording cap.
type voiceSettings struct {
	svc     VoiceService
	phrases []string
	limit   int64
}

// setVoice is config.SetVoice, as a variable so a test can make the write fail.
var setVoice = config.SetVoice

// voiceNow is speech input in force: the last change PUT applied, or the options of the start.
func (s *Server) voiceNow() voiceSettings {
	s.voiceMu.RLock()
	live := s.voiceLive
	s.voiceMu.RUnlock()
	if live != nil {
		return *live
	}
	limit := s.opts.MaxAudioBytes
	if limit <= 0 {
		limit = defaultMaxAudioBytes
	}
	return voiceSettings{svc: s.opts.Voice, phrases: s.opts.WakePhrases, limit: limit}
}

// savedVoice is the voice block of a loaded configuration, as the page sees it.
func savedVoice(cfg config.Config) *voiceSaved {
	phrases := cfg.Voice.WakePhrases
	if phrases == nil {
		phrases = []string{}
	}
	base := strings.TrimSpace(cfg.Voice.BaseURL)
	endpoint := cfg.VoiceBaseURL()
	return &voiceSaved{
		Enabled: cfg.Voice.Enabled, BaseURL: base, Endpoint: endpoint,
		Inherits: base == "" && endpoint != "", Provider: cfg.LLM.Provider,
		Model: cfg.Voice.Model, HasKey: cfg.VoiceAPIKey() != "", WakePhrases: phrases,
		TTS: savedTTS(cfg),
	}
}

// voiceViewOf answers speech input in force and, when there is a file, what it says.
func (s *Server) voiceViewOf() voiceView {
	now := s.voiceNow()
	phrases := now.phrases
	if phrases == nil {
		phrases = []string{}
	}
	v := voiceView{Enabled: now.svc != nil, WakePhrases: phrases, MaxAudioBytes: now.limit, TTS: s.ttsViewOf(),
		Local: s.opts.VoiceModels != nil}
	if s.opts.ConfigPath == "" {
		return v
	}
	v.CanSave = true
	// A file that does not read is not this endpoint's failure: the page still learns whether
	// speech input works, which is what the composer needs.
	if cfg, err := loadConfig(s.opts.ConfigPath); err == nil {
		v.Saved = savedVoice(cfg)
		v.RestartNeeded = v.Saved.Enabled != v.Enabled || !slices.Equal(v.Saved.WakePhrases, phrases) ||
			v.Saved.TTS.Enabled != v.TTS.Enabled
	}
	return v
}

// handleVoice answers whether speech input is on.
func (s *Server) handleVoice(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.voiceViewOf())
}

// handlePutVoice turns speech input on or off, and changes where recordings go, from the settings.
//
// Turning it on, or pointing it somewhere else, sends what the microphone hears to a service, so
// it needs confirm in the request itself: the page asks the person first, and a client that skips
// the question is refused here rather than trusted to have asked it. Turning it off needs nothing.
// The change is saved in the configuration file and, where the gateway can build a transcriber,
// applied at once.
func (s *Server) handlePutVoice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled     bool     `json:"enabled"`
		Confirm     bool     `json:"confirm"`
		BaseURL     *string  `json:"base_url"`
		Model       *string  `json:"model"`
		APIKey      *string  `json:"api_key"`
		WakePhrases []string `json:"wake_phrases"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if s.opts.ConfigPath == "" {
		writeError(w, http.StatusConflict, "this gateway runs on the built-in defaults and has no configuration file "+
			"to save speech input in: run the setup (motita config) or start it with -config")
		return
	}
	if body.Enabled && !body.Confirm && s.voiceNeedsConfirm(body.BaseURL) {
		writeError(w, http.StatusBadRequest, "turning speech input on sends recordings to the transcription service: "+
			"it needs confirm: true, sent after the person has been told where they go")
		return
	}
	cfg, err := setVoice(s.opts.ConfigPath, config.VoiceChange{
		Enabled: body.Enabled, BaseURL: body.BaseURL, Model: body.Model, APIKey: body.APIKey,
		WakePhrases: body.WakePhrases,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.opts.VoiceFrom != nil {
		live := &voiceSettings{phrases: cfg.Voice.WakePhrases, limit: cfg.VoiceMaxAudioBytes()}
		if cfg.Voice.Enabled {
			live.svc = s.opts.VoiceFrom(cfg)
		}
		s.voiceMu.Lock()
		s.voiceLive = live
		s.voiceMu.Unlock()
	}
	s.info("speech input was changed from the settings", "enabled", cfg.Voice.Enabled,
		"endpoint", cfg.VoiceBaseURL())
	writeJSON(w, http.StatusOK, s.voiceViewOf())
}

// voiceNeedsConfirm reports whether turning speech input on with this base_url sends recordings
// somewhere the person has not agreed to yet: it is off now, or the address changes.
func (s *Server) voiceNeedsConfirm(baseURL *string) bool {
	if s.voiceNow().svc == nil {
		return true
	}
	if baseURL == nil {
		return false
	}
	cfg, err := loadConfig(s.opts.ConfigPath)
	return err != nil || strings.TrimSpace(*baseURL) != strings.TrimSpace(cfg.Voice.BaseURL)
}

// handleTranscribe transcribes the recording in the request body.
//
// The body is the raw recording, typed by its Content-Type (what MediaRecorder produced), not a
// form: the browser has a Blob, and sending it as is avoids building a multipart body twice.
// With ?wake=1 the answer also says which wake phrase, if any, opened the transcript.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	now := s.voiceNow()
	if now.svc == nil {
		writeError(w, http.StatusNotImplemented, "speech input is off: turn it on in the settings (Voice) "+
			"or set voice.enabled in the configuration")
		return
	}
	ctype := r.Header.Get(headerContentType)
	if _, ok := voice.Extension(ctype); !ok {
		writeError(w, http.StatusUnsupportedMediaType, "the recording type "+ctype+" is not supported")
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, now.limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "the recording is larger than voice.max_audio_kb")
			return
		}
		writeError(w, http.StatusBadRequest, "the recording could not be read: "+err.Error())
		return
	}
	if len(audio) == 0 {
		writeError(w, http.StatusBadRequest, voice.ErrEmpty.Error())
		return
	}
	text, err := now.svc.Transcribe(r.Context(), audio, ctype)
	if err != nil {
		s.warn("a transcription failed", "error", err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := transcription{Text: text}
	if r.URL.Query().Get("wake") == "1" {
		if wake, ok := voice.Match(text, now.phrases); ok {
			out.Wake = &wake
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWake looks for a wake phrase at the start of a text the page transcribed itself (with a
// model it runs, see voicemodels.go), the way POST /v1/voice/transcribe?wake=1 does with its own:
// the phrases and how they match stay here, whoever listened. It needs no transcription service.
func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	out := transcription{Text: body.Text}
	if wake, ok := voice.Match(body.Text, s.voiceNow().phrases); ok {
		out.Wake = &wake
	}
	writeJSON(w, http.StatusOK, out)
}
