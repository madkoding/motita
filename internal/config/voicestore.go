package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// VoiceKeyVariable is the variable the transcription key is kept under in the credentials file
// beside the configuration, the same name the environment reads it from.
const VoiceKeyVariable = "MOTITA_VOICE_API_KEY"

// VoiceChange is what the browser's settings change in the voice block. A nil field is left as
// the file has it.
type VoiceChange struct {
	Enabled bool
	BaseURL *string
	Model   *string
	// APIKey goes to the credentials file, never into the YAML; "" removes it.
	APIKey      *string
	WakePhrases []string
}

// The steps of SetVoice that touch the credentials, as variables so a test can make them fail.
var (
	setVoiceCredential = SetCredential
)

// SetVoice writes a voice change into the configuration file at path, IN PLACE, and answers the
// configuration as it now loads.
//
// Like SetUILanguage it edits lines rather than re-marshalling. The file is then loaded whole, and
// a change that leaves it invalid (on a provider with no transcription endpoint, voice.base_url is
// required) is undone before the error is answered: a setting that broke the next start is the one
// outcome worse than an error.
func SetVoice(path string, ch VoiceChange) (Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setVoiceText(string(body), ch)
	if err != nil {
		return Config{}, err
	}
	mode := info.Mode().Perm()
	if err := writeConfigAtomic(path, []byte(text), mode); err != nil {
		return Config{}, err
	}
	cfg, err := LoadWithoutKey(path)
	if err == nil && cfg.Voice.Enabled != ch.Enabled {
		err = fmt.Errorf("MOTITA_VOICE_ENABLED in the gateway's environment overrides voice.enabled: " +
			"remove it from the environment, or change it there")
	}
	if err == nil && ch.APIKey != nil {
		// The new key is stored after the file, so the load above validated the old one. Checked
		// here against the new key, so a key bound for a plain-http remote endpoint is refused
		// before anything is kept, instead of leaving a configuration that no longer loads.
		probe := cfg
		probe.Voice.APIKey = strings.TrimSpace(*ch.APIKey)
		err = probe.validateVoice()
	}
	if err != nil {
		if rerr := writeConfigAtomic(path, body, mode); rerr != nil {
			return Config{}, fmt.Errorf("%w (and the file could not be restored: %v)", err, rerr)
		}
		return Config{}, err
	}
	if ch.APIKey == nil {
		return cfg, nil
	}
	if err := setVoiceCredential(path, VoiceKeyVariable, strings.TrimSpace(*ch.APIKey)); err != nil {
		return Config{}, err
	}
	return LoadWithoutKey(path)
}

// setVoiceText is SetVoice on the text alone.
//
// A missing key is added as the block's FIRST entry, so the keys are written last to first: a new
// block then reads enabled, base_url, model, api_key, wake_phrases, the order the reference uses.
func setVoiceText(body string, ch VoiceChange) (string, error) {
	text, err := body, error(nil)
	if ch.WakePhrases != nil {
		phrases := make([]string, 0, len(ch.WakePhrases))
		for _, p := range ch.WakePhrases {
			if p = strings.TrimSpace(p); p != "" {
				phrases = append(phrases, p)
			}
		}
		text, err = setBlockKeyText(text, "voice", "wake_phrases", flowList(phrases))
	}
	// A key written in the YAML would win over the one stored beside it, so the new key would be
	// ignored without a word: the YAML's is cleared when the page sends one.
	if err == nil && ch.APIKey != nil {
		text, err = setBlockKeyText(text, "voice", "api_key", `""`)
	}
	if err == nil && ch.Model != nil {
		text, err = setBlockKeyText(text, "voice", "model", strconv.Quote(strings.TrimSpace(*ch.Model)))
	}
	if err == nil && ch.BaseURL != nil {
		text, err = setBlockKeyText(text, "voice", "base_url", strconv.Quote(strings.TrimSpace(*ch.BaseURL)))
	}
	if err == nil {
		text, err = setBlockKeyText(text, "voice", "enabled", strconv.FormatBool(ch.Enabled))
	}
	return text, err
}
