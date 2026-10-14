package app

import (
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/voice"
)

// TestVoiceForFollowsTheConfiguration: off means no service at all (an untyped nil the gateway
// reads as off), and on means a transcriber pointed at the resolved endpoint.
func TestVoiceForFollowsTheConfiguration(t *testing.T) {
	cfg := config.Default()
	if voiceFor(cfg) != nil {
		t.Fatal("speech input is off by default")
	}
	cfg.Voice.Enabled = true
	cfg.Voice.BaseURL = "http://127.0.0.1:8080/v1/"
	cfg.Voice.Language = "es"
	got, ok := voiceFor(cfg).(voice.Transcriber)
	if !ok {
		t.Fatalf("service = %T", voiceFor(cfg))
	}
	want := voice.Transcriber{BaseURL: "http://127.0.0.1:8080/v1", Model: "whisper-1", Language: "es"}
	if got.BaseURL != want.BaseURL || got.Model != want.Model || got.Language != want.Language || got.APIKey != "" {
		t.Errorf("transcriber = %+v", got)
	}
}

// TestSpeechForFollowsTheConfiguration: off means no voice at all, and on means a speaker pointed
// at the resolved endpoint with its own key.
func TestSpeechForFollowsTheConfiguration(t *testing.T) {
	cfg := config.Default()
	if speechFor(cfg) != nil {
		t.Fatal("the gateway's voice is off by default")
	}
	cfg.Voice.TTS.Enabled = true
	cfg.Voice.TTS.BaseURL = "http://127.0.0.1:8880/v1/"
	cfg.Voice.TTS.APIKey = "k"
	got, ok := speechFor(cfg).(voice.Speaker)
	if !ok {
		t.Fatalf("service = %T", speechFor(cfg))
	}
	if got.BaseURL != "http://127.0.0.1:8880/v1" || got.APIKey != "k" || got.Model != "gpt-4o-mini-tts" || got.Format != "mp3" {
		t.Errorf("speaker = %+v", got)
	}
}
