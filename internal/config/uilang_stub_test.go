package config

import "testing"

// The stub only says what it cannot do; the real implementation replaces it with its own tests.
func TestSetUILanguageStubRefuses(t *testing.T) {
	if err := SetUILanguage("motita.yaml", "es"); err == nil {
		t.Fatal("the stub must refuse rather than pretend the setting was saved")
	}
}
