package redact

import (
	"strings"
	"testing"
)

func TestStringMasksSecrets(t *testing.T) {
	// Provider-shaped fixtures are split with "+" so secret scanners do not
	// flag these fake values as leaked credentials.
	cases := []struct {
		name, in, want string
	}{
		{"authorization bearer header", `curl -H "Authorization: Bearer abc.def-123"`,
			`curl -H "Authorization: Bearer [REDACTED]"`},
		{"authorization basic header", "Authorization: Basic dXNlcjpwYXNz", "Authorization: Basic [REDACTED]"},
		{"authorization raw value", "authorization=s3cr3tvalue0123456789", "authorization=[REDACTED]"},
		{"bearer outside a header", "token is Bearer eyJhbGciOiJIUzI1NiJ9.x", "token is Bearer [REDACTED]"},
		{"openai key", "OPENAI_API_KEY=sk-proj-abcdefghijklmnop1234", "OPENAI_API_KEY=[REDACTED]"},
		{"anthropic key", "key sk-ant-api03-abcdefghijklmnopqrst done", "key [REDACTED] done"},
		{"github classic token", "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED]"},
		{"github oauth token", "using gho_abcdefghijklmnopqrstuvwx", "using [REDACTED]"},
		{"github fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghij", "[REDACTED]"},
		{"aws access key", "aws " + "AKIA" + "IOSFODNN7EXAMPLE ok", "aws [REDACTED] ok"},
		{"google api key", "AIza" + "SyA-abcdefghijklmnopqrstuvwxyz01234", "[REDACTED]"},
		{"slack token", "xoxb" + "-1234567890-abcdefghij", "[REDACTED]"},
		{"url credentials", "cloning https://user:hunter2@github.com/o/r.git",
			"cloning https://[REDACTED]@github.com/o/r.git"},
		{"secret query params", "GET /x?a=1&access_token=abc&api_key=def&password=ghi#frag",
			"GET /x?a=1&access_token=[REDACTED]&api_key=[REDACTED]&password=[REDACTED]#frag"},
		{"bare query string", "token=abc&state=xyz", "token=[REDACTED]&state=xyz"},
		{"env file past the first line", "FOO=1\nOLLAMA_API_KEY=abcdef123456\n  DB_PASSWORD=hunter2hunter2\n",
			"FOO=1\nOLLAMA_API_KEY=[REDACTED]\n  DB_PASSWORD=[REDACTED]\n"},
		{"exported variable", "export OPENAI_API_KEY=zzzzzzzzzzzzzzzz", "export OPENAI_API_KEY=[REDACTED]"},
		{"quoted value", "export OLLAMA_API_KEY='abc123'\nSECRET=\"xyz\"",
			"export OLLAMA_API_KEY='[REDACTED]'\nSECRET=\"[REDACTED]\""},
		{"command line flag", "mysql --password=hunter2 db", "mysql --password=[REDACTED] db"},
		{
			"json credentials",
			`{"access_token": "eyJabc.def", "refresh_token":"1//0gabc", "client_secret": "GOCSPX-abc", ` +
				`"apiKey": "k1", "token_type": "Bearer"}`,
			`{"access_token": "[REDACTED]", "refresh_token":"[REDACTED]", "client_secret": "[REDACTED]", ` +
				`"apiKey": "[REDACTED]", "token_type": "Bearer"}`,
		},
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
		`curl -H "Authorization: Bearer ${TOKEN}" "https://x/?token=$(cat t)&key={{.Key}}"`,
		"Authorization: short",
		`{"max_tokens": 4096, "key": "value", "token": "${TOKEN}", "input_tokens": "12"}`,
		"PATH=/usr/bin\nHOME=/home/u",
		"best = max(pixels, key=lambda p: p[1])\nrows = sorted(rows,\n    key=len)",
		`<g data-edge-key="a-b" key="x">`,
		"body := `{\"api_key\":\"` + key + `\"}`\nlog(`\"token\": \"`)\nx := 1 // \"",
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

func TestValuesMasksTheLongestFirstAndSkipsEmpty(t *testing.T) {
	got := Values("a=abcd b=abcdef", []string{"abcd", "", "abcdef"})
	if got != "a="+Mask+" b="+Mask {
		t.Errorf("got %q", got)
	}
}
