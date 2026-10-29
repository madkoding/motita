package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/doctor"
	"github.com/madkoding/motita/internal/voice"
)

// runDoctor is `motita doctor`: the machine's checks, then the configuration this run would
// read and the sandbox it would build from it. It exits with RunError when a check fails, so a
// script can tell a working machine from a broken one.
func (op Options) runDoctor(fl flags) int {
	env := doctor.System(op.Version, op.ExePath)
	if op.DoctorEnv != nil {
		env = *op.DoctorEnv
	}
	checks := append(doctor.Environment(env), op.doctorConfig(fl)...)
	if doctor.Write(op.Out, checks) {
		return RunError
	}
	return Success
}

// doctorConfig checks the configuration file and the sandbox built from it. It reads the
// same file a run would (resolvedConfigPath), and like -isolation it opens no log file, so it
// leaves nothing behind.
func (op Options) doctorConfig(fl flags) []doctor.Check {
	path := resolvedConfigPath(fl)
	if path == "" {
		return []doctor.Check{{Name: "config", Status: doctor.Warn, Detail: "no configuration file yet: run motita config"}}
	}
	cfg, err := loadPreferringKey(path, fl)
	if err != nil {
		return []doctor.Check{{Name: "config", Status: doctor.Fail, Detail: fmt.Sprintf("%s: %v", path, err)}}
	}
	checks := []doctor.Check{{Name: "config", Detail: path + ": " + DescribeConfig(cfg)},
		voiceCheck(cfg), speechCheck(cfg), localSpeechCheck(cfg), localVoicesCheck(voiceLibrary())}

	cfg.Agent.LogFile = ""
	cfg.Agent.LogConsole = false
	log, err := op.newLogger(cfg.Agent)
	if err != nil {
		return append(checks, doctor.Check{Name: "sandbox", Status: doctor.Fail, Detail: err.Error()})
	}
	defer log.Close()
	opts := SandboxOptions(cfg, log)
	opts.HiddenPaths = config.SecretFiles(path)
	box, err := op.newSandbox(opts)
	if err != nil {
		return append(checks, doctor.Check{
			Name: "sandbox", Status: doctor.Fail, Detail: "could not prepare the sandbox: " + err.Error(),
		})
	}
	defer box.Close()
	checks = append(checks, doctor.Check{Name: "sandbox", Detail: fmt.Sprintf("isolation %v", box.Isolation())})
	for _, s := range box.NotApplied() {
		checks = append(checks, doctor.Check{Name: "sandbox", Status: doctor.Warn, Detail: "not applied: " + s})
	}
	return checks
}

// voiceCheck says whether speech input is on and where recordings go, or how to turn it on.
func voiceCheck(cfg config.Config) doctor.Check {
	if !cfg.Voice.Enabled {
		return doctor.Check{Name: "voice", Status: doctor.Warn,
			Detail: "speech input is off: turn it on in the web interface (Settings, Voice) or set voice.enabled: true"}
	}
	detail := fmt.Sprintf("speech input on: %s, model %s", cfg.VoiceBaseURL(), cfg.Voice.Model)
	if len(cfg.Voice.WakePhrases) == 0 {
		detail += "; no wake phrases, so hands-free listening is unavailable"
	}
	return doctor.Check{Name: "voice", Detail: detail}
}

// speechCheck says whether the gateway's voice is on and where the text of answers goes, or that
// the device's own voices are used. Like the rest of the doctor it calls no service.
func speechCheck(cfg config.Config) doctor.Check {
	tts := cfg.Voice.TTS
	if !tts.Enabled {
		return doctor.Check{Name: "speech", Detail: "answers are read with the voices of each device; " +
			"set voice.tts.enabled: true for the gateway's own voice"}
	}
	detail := fmt.Sprintf("the gateway's voice is on: %s, model %s, voices %s", cfg.TTSBaseURL(), tts.Model,
		strings.Join(cfg.TTSVoices(), ", "))
	if cfg.TTSAPIKey() == "" && !localAddress(cfg.TTSBaseURL()) {
		return doctor.Check{Name: "speech", Status: doctor.Warn,
			Detail: detail + "; no key for a remote endpoint: set voice.tts.api_key or MOTITA_VOICE_TTS_API_KEY"}
	}
	return doctor.Check{Name: "speech", Detail: detail}
}

// detectSpeech is voice.DetectSpeech, as a variable so a test need not listen on the common ports.
var detectSpeech = voice.DetectSpeech

// localSpeechCheck looks, on this machine only, for a server that speaks the OpenAI speech API
// (/audio/speech) and says where to turn it on. The probe carries no text; it only asks whether the
// route exists. Nothing is switched on: sending answers to it is the person's choice.
func localSpeechCheck(cfg config.Config) doctor.Check {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found := detectSpeech(ctx, cfg.TTSBaseURL())
	if len(found) == 0 {
		return doctor.Check{Name: "speech", Detail: "no speech server (/audio/speech) found on this machine"}
	}
	bases := make([]string, len(found))
	for i, f := range found {
		bases[i] = f.Base
	}
	detail := "speech server found on this machine: " + strings.Join(bases, ", ")
	if cfg.Voice.TTS.Enabled {
		return doctor.Check{Name: "speech", Detail: detail}
	}
	return doctor.Check{Name: "speech",
		Detail: detail + "; use it in Settings, Read aloud, Voice service (or set voice.tts.enabled and base_url)"}
}

// localVoicesCheck names the local voices downloaded and hashes their files again: a file that
// changed on disk since it was checked would be served to the page as it is.
func localVoicesCheck(lib *voice.Library) doctor.Check {
	if lib == nil {
		return doctor.Check{Name: "voices", Detail: "no home directory to keep local voices in"}
	}
	var names, bad []string
	for _, m := range lib.Models {
		if !lib.Installed(m.ID) {
			continue
		}
		if err := lib.Verify(m.ID); err != nil {
			bad = append(bad, m.ID+": "+err.Error())
			continue
		}
		names = append(names, m.ID)
	}
	if len(bad) > 0 {
		return doctor.Check{Name: "voices", Status: doctor.Warn,
			Detail: strings.Join(bad, "; ") + "; delete the voice in Settings, Voice and download it again"}
	}
	if len(names) == 0 {
		return doctor.Check{Name: "voices",
			Detail: "no local voice downloaded; Settings, Voice downloads one to run in the browser"}
	}
	return doctor.Check{Name: "voices",
		Detail: "local voices downloaded and matching their hashes: " + strings.Join(names, ", ")}
}

// localAddress reports whether an endpoint is on this machine, where a server needs no key.
func localAddress(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || net.ParseIP(h).IsLoopback()
}
