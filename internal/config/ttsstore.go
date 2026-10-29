package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// TTSKeyVariable is the variable the speech key is kept under in the credentials file beside the
// configuration, the same name the environment reads it from.
const TTSKeyVariable = "MOTITA_VOICE_TTS_API_KEY"

// TTSChange is what the browser's settings change in the voice.tts block. A nil field is left as
// the file has it.
type TTSChange struct {
	Enabled bool
	API     *string
	BaseURL *string
	Model   *string
	Voice   *string
	// APIKey goes to the credentials file, never into the YAML; "" removes it.
	APIKey *string
}

// setTTSCredential is SetCredential, as a variable so a test can make the write fail.
var setTTSCredential = SetCredential

// SetTTS writes a voice.tts change into the configuration file at path, IN PLACE, and answers the
// configuration as it now loads. It follows SetVoice: lines are edited rather than re-marshalled,
// and a change that leaves the file invalid is undone before the error is answered.
func SetTTS(path string, ch TTSChange) (Config, error) {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return Config{}, fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setTTSText(string(body), ch)
	if err != nil {
		return Config{}, err
	}
	mode := info.Mode().Perm()
	// The key is stored BEFORE the file is loaded: a DashScope block is invalid without one, and the
	// load is what validates the change. Both are put back when it is refused.
	creds := CredentialsPath(path)
	oldKey := readCredentials(creds)[TTSKeyVariable]
	if ch.APIKey != nil {
		if err := setTTSCredential(path, TTSKeyVariable, strings.TrimSpace(*ch.APIKey)); err != nil {
			return Config{}, err
		}
	}
	undo := func(cause error) (Config, error) {
		if ch.APIKey != nil {
			if rerr := setTTSCredential(path, TTSKeyVariable, oldKey); rerr != nil {
				cause = fmt.Errorf("%w (and the key could not be restored: %v)", cause, rerr)
			}
		}
		return Config{}, cause
	}
	if err := writeConfigAtomic(path, []byte(text), mode); err != nil {
		return undo(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err == nil && cfg.Voice.TTS.Enabled != ch.Enabled {
		err = fmt.Errorf("MOTITA_VOICE_TTS_ENABLED in the gateway's environment overrides voice.tts.enabled: " +
			"remove it from the environment, or change it there")
	}
	if err != nil {
		if rerr := writeConfigAtomic(path, body, mode); rerr != nil {
			err = fmt.Errorf("%w (and the file could not be restored: %v)", err, rerr)
		}
		return undo(err)
	}
	return cfg, nil
}

// setTTSText is SetTTS on the text alone. Keys are written last to first so a new block reads
// enabled, api, base_url, model, voice, api_key.
func setTTSText(body string, ch TTSChange) (string, error) {
	text, err := body, error(nil)
	set := func(key, value string) {
		if err == nil {
			text, err = setNestedKeyText(text, "voice", "tts", key, value)
		}
	}
	if ch.APIKey != nil {
		// A key written in the YAML would win over the one stored beside it.
		set("api_key", `""`)
	}
	for _, f := range []struct {
		key string
		val *string
	}{{"voice", ch.Voice}, {"model", ch.Model}, {"base_url", ch.BaseURL}, {"api", ch.API}} {
		if f.val != nil {
			set(f.key, strconv.Quote(strings.TrimSpace(*f.val)))
		}
	}
	set("enabled", strconv.FormatBool(ch.Enabled))
	return text, err
}

// setNestedKeyText sets key under block.sub, on the text: the same edit setBlockKeyText makes for a
// top-level block, one level down. A missing sub-block (and block) is added.
func setNestedKeyText(body, block, sub, key, value string) (string, error) {
	lines := strings.Split(body, "\n")
	start, inline := findBlockStart(lines, block)
	if inline != "" {
		return "", fmt.Errorf("the configuration writes %s as an inline value (%q): change %s.%s.%s by hand",
			block, inline, block, sub, key)
	}
	if start < 0 {
		added := block + ":\n  " + sub + ":\n    " + key + ": " + value + "\n"
		trimmed := strings.TrimRight(body, "\n")
		if trimmed == "" {
			return added, nil
		}
		return trimmed + "\n\n" + added, nil
	}
	i, indent, _ := findBlockKey(lines, start, sub)
	if i < 0 {
		if indent == "" {
			indent = "  "
		}
		out := append([]string{}, lines[:start+1]...)
		out = append(out, indent+sub+":")
		lines = append(out, lines[start+1:]...)
		i = start + 1
	}
	header := strings.TrimSpace(lines[i])
	if rest := strings.TrimSpace(strings.TrimPrefix(header, sub+":")); rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("the configuration writes %s.%s as an inline value (%q): change %s.%s.%s by hand",
			block, sub, rest, block, sub, key)
	}
	pad := indentOf.FindString(lines[i])
	end := i + 1
	for end < len(lines) && belongsToValue(lines[end], pad) {
		end++
	}
	mini := []string{sub + ":"}
	for _, l := range lines[i+1 : end] {
		mini = append(mini, strings.TrimPrefix(l, pad))
	}
	// The block opens with a bare header, the only case setBlockKeyText refuses.
	edited, _ := setBlockKeyText(strings.Join(mini, "\n"), sub, key, value)
	out := append([]string{}, lines[:i]...)
	for n, l := range strings.Split(edited, "\n") {
		if n == 0 {
			l = lines[i] // keeps the header's own comment
		} else if strings.TrimSpace(l) != "" {
			l = pad + l
		}
		out = append(out, l)
	}
	return strings.Join(append(out, lines[end:]...), "\n"), nil
}
