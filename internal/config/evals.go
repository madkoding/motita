package config

import "path/filepath"

// EvalsFile is where the results of the model checks are kept: ~/.motita/evals.json. Empty without a
// home.
func EvalsFile() string {
	if d := Dir(); d != "" {
		return filepath.Join(d, "evals.json")
	}
	return ""
}
