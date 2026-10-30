package config

import (
	"strings"
	"testing"
)

// The memory only grows when the model is told to write to it: the task prompt has to say when, and
// must not call the memory "the notes", which no longer exist.
func TestTheTaskPromptTellsTheModelToSaveToTheMemory(t *testing.T) {
	for _, want := range []string{
		"SAVE, do not only read", "\"kind\": \"remember\"", "\"kind\": \"recall\"", "in:procedures",
	} {
		if !strings.Contains(executeUserPrompt, want) {
			t.Errorf("the task prompt must tell the model when to save to the memory; missing %q", want)
		}
	}
	if strings.Contains(executeUserPrompt, "the notes, threads") {
		t.Error("the task prompt still calls the memory \"the notes\"")
	}
}
