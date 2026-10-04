package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/madkoding/motita/internal/i18n"
)

// UI is how motita's interfaces present themselves to the person using them.
//
// It is a block of its own rather than a gateway or TUI setting because the same choice applies
// to every face of the program: the terminal, the browser and the setup wizard all read it, and a
// language picked in one of them is the language of all of them.
type UI struct {
	// Language is the language the interfaces speak: "auto" (the default) follows the
	// environment - the terminal's locale, the browser's language - and "en" or "es" pins one.
	Language string `yaml:"language"`
}

// validateUI checks the interface settings.
func (c *Config) validateUI() error {
	if !i18n.Valid(c.UI.Language) {
		return fmt.Errorf("ui.language is %q: it must be auto, en or es", c.UI.Language)
	}
	return nil
}

// The steps of SetUILanguage that touch the disk, as variables so a test can make each one fail:
// a full disk, a directory that is not writable, a rename refused by the filesystem.
var (
	uiCreateTemp = os.CreateTemp
	uiRename     = os.Rename
)

var (
	uiBlockLine = regexp.MustCompile(`^ui:\s*(#.*)?$`)
	uiAnyBlock  = regexp.MustCompile(`^ui:`)
	uiLangLine  = regexp.MustCompile(`^(\s+)language:\s*([^#]*?)(\s*#.*)?$`)
	uiIndented  = regexp.MustCompile(`^(\s+)\S`)
)

// SetUILanguage writes lang as ui.language into the configuration file at path, IN PLACE.
//
// The file is not re-marshalled: it is hand-edited, written by the setup wizard with comments
// that explain every block, and a round trip through a YAML encoder would throw all of that away
// to change one word. So only the one line moves - the value of an existing `language:` under
// `ui:`, or a new `language:` line under an existing `ui:`, or a new `ui:` block at the end - and
// every other byte of the file stays as it was. The write is atomic (a temporary file beside the
// original, then a rename) and keeps the file's mode, because the configuration may sit next to
// secrets and be readable by its owner only.
func SetUILanguage(path, lang string) error {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" || !i18n.Valid(lang) {
		return fmt.Errorf("the language %q is not one of auto, en, es", lang)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	updated, err := setUILanguageText(string(body), lang)
	if err != nil {
		return err
	}
	return writeConfigAtomic(path, []byte(updated), info.Mode().Perm())
}

// setUILanguageText is SetUILanguage on the text alone.
func setUILanguageText(body, lang string) (string, error) {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if uiBlockLine.MatchString(l) {
			start = i
			break
		}
		if uiAnyBlock.MatchString(l) {
			// `ui: {language: en}` and its kind: a flow mapping cannot be edited one line at a
			// time, and rewriting it is exactly the re-marshalling this function exists to avoid.
			return "", fmt.Errorf("the configuration writes ui as an inline value (%q): change ui.language by hand", strings.TrimSpace(l))
		}
	}
	if start < 0 {
		// No block yet: one is added at the end, after the file's own last line.
		trimmed := strings.TrimRight(body, "\n")
		block := "ui:\n  language: " + lang + "\n"
		if trimmed == "" {
			return block, nil
		}
		return trimmed + "\n\n" + block, nil
	}
	indent := "  "
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		m := uiIndented.FindStringSubmatch(l)
		if m == nil {
			break // the next top-level key: the block ended without a language line
		}
		indent = m[1]
		if lm := uiLangLine.FindStringSubmatch(l); lm != nil {
			lines[i] = lm[1] + "language: " + lang + lm[3]
			return strings.Join(lines, "\n"), nil
		}
	}
	out := append([]string{}, lines[:start+1]...)
	out = append(out, indent+"language: "+lang)
	out = append(out, lines[start+1:]...)
	return strings.Join(out, "\n"), nil
}

// writeConfigAtomic replaces path with body, through a temporary file in the same directory so a
// reader never sees half a file and a crash never leaves none.
func writeConfigAtomic(path string, body []byte, mode os.FileMode) error {
	tmp, err := uiCreateTemp(filepath.Dir(path), ".motita-config-*")
	if err != nil {
		return fmt.Errorf("the configuration file cannot be written: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = uiRename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("the configuration file cannot be written: %w", werr)
	}
	return nil
}
