package config

import (
	"strconv"
	"strings"
)

// ProviderLocal is Motita's own local model (internal/localmodel): llama-server on this machine,
// installed, started and stopped from the settings. Its endpoint and key are written to the
// credentials file each time the server starts (MOTITA_LOCAL_BASE_URL, MOTITA_LOCAL_API_KEY), with
// the context it was started with.
const ProviderLocal = "local"

// LocalContextVariable is the context window the local server was last started with.
const LocalContextVariable = "MOTITA_LOCAL_CONTEXT"

// isLocalProvider reports whether provider is Motita's own local model.
func isLocalProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), ProviderLocal)
}

// localWindow is the context the local server runs with, or 0 when it was never started.
func localWindow() int {
	n, err := strconv.Atoi(strings.TrimSpace(storedValue(LocalContextVariable)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
