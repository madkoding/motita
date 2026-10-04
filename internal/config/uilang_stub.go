package config

import "errors"

// SetUILanguage saves ui.language in the configuration file at path.
//
// TODO(i18n): temporary stub so the interface's /language compiles on this branch; the real
// implementation (writing the ui block of the file) lands with the configuration side of the
// feature and replaces this file when the branches are merged.
func SetUILanguage(path, lang string) error {
	return errors.New("saving ui.language is not available in this build")
}
