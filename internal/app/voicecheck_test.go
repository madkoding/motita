package app

import (
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/doctor"
)

// TestVoiceCheck: the doctor says how to turn speech input on, or where recordings go.
func TestVoiceCheck(t *testing.T) {
	cfg := config.Default()
	if c := voiceCheck(cfg); c.Status != doctor.Warn || !strings.Contains(c.Detail, "Settings") {
		t.Errorf("off = %+v", c)
	}
	cfg.Voice.Enabled = true
	if c := voiceCheck(cfg); c.Status != doctor.OK || !strings.Contains(c.Detail, "whisper-1") ||
		!strings.Contains(c.Detail, "no wake phrases") {
		t.Errorf("on = %+v", c)
	}
	cfg.Voice.WakePhrases = []string{"oye motita"}
	if c := voiceCheck(cfg); strings.Contains(c.Detail, "wake") {
		t.Errorf("with phrases = %+v", c)
	}
}

// TestAnnounceVoiceOnlyWhenOff: nothing is said about a voice already on, or a file that does not read.
func TestAnnounceVoiceOnlyWhenOff(t *testing.T) {
	t.Setenv("MOTITA_VOICE_ENABLED", "")
	op, out, _ := netOptions("")
	op.announceVoice(netConfig(t, "voice:\n  enabled: true\n"))
	op.announceVoice(netConfig(t, "llm: [\n"))
	if out.Len() != 0 {
		t.Errorf("said: %s", out)
	}
}

// TestSpeechCheckSaysWhoSpeaks: the device by default; the gateway's endpoint when on, with a
// warning when a remote endpoint has no key, and none for one on this machine.
func TestSpeechCheckSaysWhoSpeaks(t *testing.T) {
	cfg := config.Default()
	if c := speechCheck(cfg); c.Status != doctor.OK || !strings.Contains(c.Detail, "voices of each device") {
		t.Errorf("off: %+v", c)
	}
	cfg.Voice.TTS.Enabled = true
	cfg.Voice.TTS.BaseURL = "https://speech.example/v1"
	if c := speechCheck(cfg); c.Status != doctor.Warn || !strings.Contains(c.Detail, "no key") {
		t.Errorf("remote without a key: %+v", c)
	}
	cfg.Voice.TTS.APIKey = "k"
	if c := speechCheck(cfg); c.Status != doctor.OK || !strings.Contains(c.Detail, "speech.example") ||
		!strings.Contains(c.Detail, "alloy") {
		t.Errorf("remote with a key: %+v", c)
	}
	cfg.Voice.TTS.APIKey = ""
	for _, base := range []string{"http://127.0.0.1:8880/v1", "http://localhost:8880/v1"} {
		cfg.Voice.TTS.BaseURL = base
		if c := speechCheck(cfg); c.Status != doctor.OK {
			t.Errorf("%s needs no key: %+v", base, c)
		}
	}
	if localAddress("http://a b\x7f") || localAddress("https://speech.example") {
		t.Error("only this machine is local")
	}
}
