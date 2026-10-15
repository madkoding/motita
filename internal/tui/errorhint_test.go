package tui

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/llm"
)

// A provider's failure is shown in the interface's language, with the hint that follows it.
func TestErrorTextTranslatesProviderFailures(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Lang = i18n.ES
	tu.Runner = &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	got := tu.errorText(&llm.HTTPError{Code: 401, Body: `{"error":{"code":"invalid_api_key"}}`})
	if !strings.Contains(got, "El proveedor rechazó la clave de API") || strings.Contains(got, "{") {
		t.Errorf("the failure must be readable and in Spanish: %q", got)
	}
	if !strings.Contains(got, "/config") {
		t.Errorf("the hint must follow: %q", got)
	}
	// The unreachable server keeps the hint that names the fix.
	tu.Lang = i18n.EN
	got = tu.errorText(llm.Explain(&net.OpError{Op: "dial", Err: errors.New("connection refused")}))
	if !strings.Contains(got, "Could not reach the provider") || !strings.Contains(got, "hint:") {
		t.Errorf("an unreachable provider: %q", got)
	}
}
