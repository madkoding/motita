package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"

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

// voiceView is what GET /v1/voice answers: whether speech input is available, and the phrases
// the hands-free mode listens for.
type voiceView struct {
	Enabled       bool     `json:"enabled"`
	WakePhrases   []string `json:"wake_phrases"`
	MaxAudioBytes int64    `json:"max_audio_bytes"`
}

// transcription is what POST /v1/voice/transcribe answers.
type transcription struct {
	Text string `json:"text"`
	// Wake is the phrase found at the start of Text, when the request asked for one and one
	// was there. Absent otherwise.
	Wake *voice.Wake `json:"wake,omitempty"`
}

// maxAudioBytes is the recording cap in force.
func (s *Server) maxAudioBytes() int64 {
	if s.opts.MaxAudioBytes > 0 {
		return s.opts.MaxAudioBytes
	}
	return defaultMaxAudioBytes
}

// handleVoice answers whether speech input is on.
func (s *Server) handleVoice(w http.ResponseWriter, _ *http.Request) {
	phrases := s.opts.WakePhrases
	if phrases == nil {
		phrases = []string{}
	}
	writeJSON(w, http.StatusOK, voiceView{
		Enabled: s.opts.Voice != nil, WakePhrases: phrases, MaxAudioBytes: s.maxAudioBytes(),
	})
}

// handleTranscribe transcribes the recording in the request body.
//
// The body is the raw recording, typed by its Content-Type (what MediaRecorder produced), not a
// form: the browser has a Blob, and sending it as is avoids building a multipart body twice.
// With ?wake=1 the answer also says which wake phrase, if any, opened the transcript.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.opts.Voice == nil {
		writeError(w, http.StatusNotImplemented, "speech input is off: set voice.enabled in the configuration")
		return
	}
	ctype := r.Header.Get(headerContentType)
	if _, ok := voice.Extension(ctype); !ok {
		writeError(w, http.StatusUnsupportedMediaType, "the recording type "+ctype+" is not supported")
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxAudioBytes()))
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
	text, err := s.opts.Voice.Transcribe(r.Context(), audio, ctype)
	if err != nil {
		s.warn("a transcription failed", "error", err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := transcription{Text: text}
	if r.URL.Query().Get("wake") == "1" {
		if wake, ok := voice.Match(text, s.opts.WakePhrases); ok {
			out.Wake = &wake
		}
	}
	writeJSON(w, http.StatusOK, out)
}
