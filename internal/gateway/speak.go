package gateway

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
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

// speechLimit is the longest text one request may carry, in characters.
func (s *Server) speechLimit() int {
	if s.opts.SpeechMaxChars > 0 {
		return s.opts.SpeechMaxChars
	}
	return defaultSpeechMaxChars
}

// ttsViewOf answers whether the gateway has a voice, and which.
func (s *Server) ttsViewOf() ttsView {
	voices := s.opts.SpeechVoices
	if voices == nil || s.opts.Speech == nil {
		voices = []string{}
	}
	return ttsView{Enabled: s.opts.Speech != nil, Voices: voices, MaxChars: s.speechLimit()}
}

// handleSpeak says the text in the request with the gateway's voice and answers the audio.
//
// The voice must be one the configuration offers (empty means the default): the page cannot make
// the gateway ask the speech service for anything the operator did not list.
func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	if s.opts.Speech == nil {
		writeError(w, http.StatusNotImplemented, "the gateway has no voice: set voice.tts.enabled in the configuration")
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
	if n, limit := utf8.RuneCountInString(text), s.speechLimit(); n > limit {
		writeError(w, http.StatusRequestEntityTooLarge,
			"the text has "+strconv.Itoa(n)+" characters, more than voice.tts.max_chars ("+strconv.Itoa(limit)+")")
		return
	}
	voices := s.opts.SpeechVoices
	name := strings.TrimSpace(body.Voice)
	switch {
	case name == "" && len(voices) > 0:
		name = voices[0]
	case name != "" && !slices.Contains(voices, name):
		writeError(w, http.StatusBadRequest, "the voice "+strconv.Quote(name)+" is not one voice.tts offers")
		return
	}
	audio, ctype, err := s.opts.Speech.Speak(r.Context(), text, name)
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
