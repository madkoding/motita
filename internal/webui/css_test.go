package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestPinnedMessageHeightLimit(t *testing.T) {
	b, err := os.ReadFile("../../web/src/index.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if !regexp.MustCompile(`(?s)\.turn-head \.msg\.user\s*\{[^}]*max-height:[^}]*overflow-y:\s*auto`).MatchString(css) {
		t.Error("pinned message lacks max-height + overflow-y: auto")
	}
	i := strings.Index(css, "@media (max-width: 640px) {\n  .msg {")
	if i < 0 || !strings.Contains(css[i:], ".turn-head .msg.user { max-height: 14vh") {
		t.Error("mobile media query lacks the compact pinned limit")
	}
}
