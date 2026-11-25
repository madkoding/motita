package config

import "log/slog"

// WorkingDirFile is the name resolvedConfigPath gives the configuration it finds by standing in a
// directory, as opposed to the user's own (File) or one named with -config. It is whatever
// repository the user happened to open, so it is not trusted with where their keys go: see
// dropProjectEndpoints.
const WorkingDirFile = "motita.yaml"

// dropProjectEndpoints puts back the default of every endpoint a file read from the working directory
// set. A repository's motita.yaml with `llm.base_url: https://attacker` would otherwise receive the
// key from the environment (or the Gemini or Copilot sign-in) the first time the repository is
// opened. The same keys in the user's own file, in a file named with -config, or in the environment
// are the user's and apply as written.
func dropProjectEndpoints(cfg *Config) []string {
	def := Default()
	var dropped []string
	for _, e := range []struct {
		name string
		dst  *string
		def  string
	}{
		{"llm.base_url", &cfg.LLM.BaseURL, def.LLM.BaseURL},
		{"voice.base_url", &cfg.Voice.BaseURL, def.Voice.BaseURL},
		{"voice.tts.base_url", &cfg.Voice.TTS.BaseURL, def.Voice.TTS.BaseURL},
	} {
		if *e.dst != e.def {
			*e.dst = e.def
			dropped = append(dropped, e.name)
		}
	}
	return dropped
}

// guardWorkingDirFile applies dropProjectEndpoints to a configuration read from path when path is the
// working-directory file, and says so.
func guardWorkingDirFile(cfg *Config, path string) {
	if path != WorkingDirFile {
		return
	}
	if dropped := dropProjectEndpoints(cfg); len(dropped) > 0 {
		slog.Warn("ignoring the endpoints a configuration found in the working directory sets; "+
			"put them in ~/.motita/motita.yaml or name the file with -config",
			"file", path, "keys", dropped)
	}
}
