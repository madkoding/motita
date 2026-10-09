package gitforge

import (
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestDefaultsAndMethods(t *testing.T) {
	all := Defaults(noEnv)
	if len(all) != 3 {
		t.Fatalf("want 3 services, got %d", len(all))
	}
	byID := map[string]Service{}
	for _, s := range all {
		byID[s.ID] = s
	}
	// GitHub works out of the box through its device flow; the others need an
	// OAuth application, and until there is one a token is the only way.
	if m := byID["github"].Methods(); len(m) != 2 || m[0] != MethodDevice || m[1] != MethodToken {
		t.Errorf("github methods = %v", m)
	}
	if !byID["github"].OAuthReady() {
		t.Error("github must be OAuth-ready by default")
	}
	for _, id := range []string{"gitlab", "bitbucket"} {
		if m := byID[id].Methods(); len(m) != 1 || m[0] != MethodToken || byID[id].OAuthReady() {
			t.Errorf("%s methods = %v", id, m)
		}
	}
}

func TestEnvironmentConfiguresTheOAuthApplication(t *testing.T) {
	env := func(k string) string {
		return map[string]string{
			"MOTITA_GITLAB_CLIENT_ID":        " gl-id ",
			"MOTITA_BITBUCKET_CLIENT_ID":     "bb-id",
			"MOTITA_BITBUCKET_CLIENT_SECRET": "bb-secret",
			"MOTITA_GITHUB_CLIENT_ID":        "mine",
		}[k]
	}
	byID := map[string]Service{}
	for _, s := range Defaults(env) {
		byID[s.ID] = s
	}
	if byID["gitlab"].Client.ID != "gl-id" || byID["gitlab"].Methods()[0] != MethodDevice {
		t.Errorf("gitlab = %+v", byID["gitlab"])
	}
	if bb := byID["bitbucket"]; bb.Client.Secret != "bb-secret" || bb.Methods()[0] != MethodCode {
		t.Errorf("bitbucket = %+v", bb)
	}
	if byID["github"].Client.ID != "mine" {
		t.Errorf("github id = %q", byID["github"].Client.ID)
	}
}

func TestOSEnvAndNilLookup(t *testing.T) {
	t.Setenv("MOTITA_GITLAB_CLIENT_ID", "from-os")
	if OSEnv("MOTITA_GITLAB_CLIENT_ID") != "from-os" {
		t.Error("OSEnv must read the process environment")
	}
	for _, s := range Defaults(nil) {
		if s.ID == "gitlab" && s.Client.ID != "from-os" {
			t.Error("a nil lookup must mean the process environment")
		}
	}
	s, err := Custom(KindGitLab, "git.corp.example", nil)
	if err != nil || s.Client.ID != "from-os" {
		t.Errorf("custom with a nil lookup: %+v %v", s, err)
	}
}

func TestCustom(t *testing.T) {
	cases := []struct {
		kind    Kind
		host    string
		id, api string
	}{
		{KindGitHub, "ghe.corp.io", "github@ghe.corp.io", "https://ghe.corp.io/api/v3"},
		{KindGitLab, "GIT.corp.io", "gitlab@git.corp.io", "https://git.corp.io/api/v4"},
		{KindGitea, "forge.corp.io", "gitea@forge.corp.io", "https://forge.corp.io/api/v1"},
	}
	for _, c := range cases {
		s, err := Custom(c.kind, c.host, noEnv)
		if err != nil || s.ID != c.id || s.APIBase != c.api || s.Kind != c.kind || s.TokenURL == "" || s.Name == "" {
			t.Errorf("%s: %+v %v", c.host, s, err)
		}
	}
	// A default host resolves to the default service, not a custom twin.
	if s, err := Custom(KindGitLab, "gitlab.com", noEnv); err != nil || s.ID != "gitlab" {
		t.Errorf("gitlab.com: %+v %v", s, err)
	}
	if _, err := Custom(KindBitbucket, "bb.corp.io", noEnv); err == nil {
		t.Error("a self-hosted bitbucket is not supported")
	}
	for _, bad := range []string{"", ".x.io", "-x.io", "a/b", "a b", "a:8080", "u@h.io", strings.Repeat("a", 260)} {
		if _, err := Custom(KindGitLab, bad, noEnv); err == nil {
			t.Errorf("host %q must be refused", bad)
		}
	}
}

func TestParseRemote(t *testing.T) {
	ok := map[string]Remote{
		"https://github.com/madkoding/motita.git":  {"github.com", "madkoding/motita"},
		"https://GitHub.com/madkoding/motita/":     {"github.com", "madkoding/motita"},
		"git@github.com:madkoding/motita.git":      {"github.com", "madkoding/motita"},
		"ssh://git@gitlab.com:22/grp/sub/repo.git": {"gitlab.com", "grp/sub/repo"},
		"  https://bitbucket.org/ws/repo  ":        {"bitbucket.org", "ws/repo"},
		"https://user:pw@gitea.example.com/o/r":    {"gitea.example.com", "o/r"},
	}
	for in, want := range ok {
		got, err := ParseRemote(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "madkoding/motita", "https://github.com/onlyowner", "https://", "http://[::1", "git@host-without-path",
		"https://github.com/o/..", "https://github.com/../r", "git@github.com:o/r?x=1", "https://github.com/o/r%3Fa", "git@host:o//r"} {
		if _, err := ParseRemote(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	r := Remote{Host: "gitlab.com", Path: "grp/sub/repo"}
	if r.Owner() != "grp/sub" || r.Name() != "repo" {
		t.Errorf("owner/name = %q %q", r.Owner(), r.Name())
	}
	if (Remote{Path: "solo"}).Owner() != "" {
		t.Error("a path with no slash has no owner")
	}
}
