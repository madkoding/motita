package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

func TestSecretsAreSortedAndMasked(t *testing.T) {
	var none Sandbox
	if none.secretEnv() == nil && len(none.SecretNames()) != 0 {
		t.Fatal("a sandbox with no secrets has no names")
	}
	s := &Sandbox{op: Options{Secrets: func() map[string]string {
		return map[string]string{"B_KEY": "value-two-long", "A_KEY": "value-one", "TINY": "ab"}
	}}}
	if got := strings.Join(s.SecretNames(), ","); got != "A_KEY,B_KEY,TINY" {
		t.Errorf("names %s", got)
	}
	if got := strings.Join(s.secretEnv(), ","); got != "A_KEY=value-one,B_KEY=value-two-long,TINY=ab" {
		t.Errorf("env %s", got)
	}
	out := s.maskSecrets("a=value-one b=value-two-long tiny=ab")
	if strings.Contains(out, "value-") || !strings.Contains(out, "tiny=ab") {
		t.Errorf("masked %q", out)
	}
	if got := none.maskSecrets("plain"); got != "plain" {
		t.Errorf("%q", got)
	}
}

// A command receives the person's variables, and the value it prints never leaves the sandbox.
func TestACommandGetsTheSecretsAndItsOutputIsMasked(t *testing.T) {
	s, err := New(Options{Dir: t.TempDir(), Timeout: 30 * time.Second, Secrets: func() map[string]string {
		return map[string]string{"DEPLOY_TOKEN": "s3cr3t-value-123"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", `[ "$DEPLOY_TOKEN" = s3cr3t-value-123 ] && echo ok:$DEPLOY_TOKEN`},
	})
	if err != nil || exit != 0 || strings.Contains(out, "s3cr3t") || !strings.Contains(out, "ok:"+"[REDACTED]") {
		t.Errorf("exit %d err %v out %q", exit, err, out)
	}
}
