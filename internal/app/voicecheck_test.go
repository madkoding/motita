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
