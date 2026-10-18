package config

import "testing"

// gpg_signing is on unless the file or the environment turns it off.
func TestGPGSigning(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_GPG_SIGNING", "")
	cfg, err := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n"))
	if err != nil || !cfg.Sandbox.GPGSigning {
		t.Fatalf("default = %v, %v", cfg.Sandbox.GPGSigning, err)
	}
	if cfg, _ := LoadWithoutKey(diskFile(t, "sandbox:\n  gpg_signing: false\n")); cfg.Sandbox.GPGSigning {
		t.Error("the file must be able to turn it off")
	}
	t.Setenv("MOTITA_SANDBOX_GPG_SIGNING", "false")
	if cfg, _ := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n")); cfg.Sandbox.GPGSigning {
		t.Error("the environment must be able to turn it off")
	}
}
