package redact

import (
	"strings"
	"testing"
)

func TestStringMasksSecrets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"authorization bearer header", `curl -H "Authorization: Bearer abc.def-123"`, `curl -H "Authorization: Bearer [REDACTED]"`},
		{"authorization basic header", "Authorization: Basic dXNlcjpwYXNz", "Authorization: Basic [REDACTED]"},
		{"authorization raw value", "authorization=s3cr3tvalue", "authorization=[REDACTED]"},
		{"bearer outside a header", "token is Bearer eyJhbGciOiJIUzI1NiJ9.x", "token is Bearer [REDACTED]"},
		{"openai key", "OPENAI_API_KEY=sk-proj-abcdefghijklmnop1234", "OPENAI_API_KEY=[REDACTED]"},
		{"anthropic key", "key sk-ant-api03-abcdefghijklmnopqrst done", "key [REDACTED] done"},
		{"github classic token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED]"},
		{"github oauth token", "using gho_abcdefghijklmnopqrstuvwx", "using [REDACTED]"},
		{"github fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghij", "[REDACTED]"},
		{"aws access key", "aws AKIAIOSFODNN7EXAMPLE ok", "aws [REDACTED] ok"},
		{"google api key", "AIzaSyA-abcdefghijklmnopqrstuvwxyz01234", "[REDACTED]"},
		{"slack token", "xoxb-1234567890-abcdefghij", "[REDACTED]"},
		{"url credentials", "cloning https://user:hunter2@github.com/o/r.git", "cloning https://[REDACTED]@github.com/o/r.git"},
		{"secret query params", "GET /x?a=1&access_token=abc&api_key=def&password=ghi#frag", "GET /x?a=1&access_token=[REDACTED]&api_key=[REDACTED]&password=[REDACTED]#frag"},
		{"bare query string", "token=abc&state=xyz", "token=[REDACTED]&state=xyz"},
		{
			"pem private key",
			"before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nIBAAK\n-----END RSA PRIVATE KEY-----\nafter",
			"before\n[REDACTED]\nafter",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := String(c.in); got != c.want {
				t.Errorf("String(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStringKeepsOrdinaryText(t *testing.T) {
	for _, in := range []string{
		"",
		"go test ./... passed",
		"the task-runner-abcdefghijklmnopqrstuvwxyz finished",
		"monkey=banana sort=key",
		"basic usage of the bearer",
		"https://github.com/madkoding/motita",
		"-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----",
	} {
		if got := String(in); got != in {
			t.Errorf("String(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestStringMasksEverySecretInALine(t *testing.T) {
	in := "git push https://x:ghp_abcdefghijklmnopqrstuvwxyz0123@github.com && echo sk-abcdefghijklmnopqrstu"
	got := String(in)
	for _, leak := range []string{"ghp_", "sk-abc"} {
		if strings.Contains(got, leak) {
			t.Errorf("String(%q) = %q, still leaks %q", in, got, leak)
		}
	}
}
