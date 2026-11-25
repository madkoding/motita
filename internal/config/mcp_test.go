package config

import (
	"strings"
	"testing"
)

func TestMCPServersLoadAndValidate(t *testing.T) {
	path := diskFile(t, `agent:
  mcp:
    - name: shop
      command: npx
      args: [-y, "@shop/mcp"]
      env: {TOKEN: abc}
    - name: db_1
      command: /usr/bin/db-mcp
      dir: /srv
`)
	cfg, err := LoadWithoutKey(path)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Agent.MCP
	if len(s) != 2 || s[0].Name != "shop" || s[0].Args[1] != "@shop/mcp" || s[0].Env["TOKEN"] != "abc" || s[1].Dir != "/srv" {
		t.Fatalf("servers = %+v", s)
	}
}

func TestMCPServersRefused(t *testing.T) {
	for body, want := range map[string]string{
		"agent:\n  mcp:\n    - command: x\n":                                                 "must be letters",
		"agent:\n  mcp:\n    - name: a b\n      command: x\n":                                "must be letters",
		"agent:\n  mcp:\n    - name: a\n      command: x\n    - name: a\n      command: y\n": "used twice",
		"agent:\n  mcp:\n    - name: a\n      command: \"  \"\n":                             "command is empty",
	} {
		if _, err := LoadWithoutKey(diskFile(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}
