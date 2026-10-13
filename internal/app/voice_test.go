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
