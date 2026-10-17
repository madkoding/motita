package config

import (
	"strings"
	"testing"
)

func TestXerjIsOffByDefaultAndNotChecked(t *testing.T) {
	c := Default()
	if c.Xerj.Enabled || c.Xerj.URL != "http://localhost:9200" || c.Xerj.Index != "ax-*" {
		t.Fatalf("default xerj = %+v", c.Xerj)
	}
	c.Xerj.URL = "not a url"
	if err := c.ValidateWithoutKey(); err != nil {
		t.Errorf("a disabled block must not be checked: %v", err)
	}
}

func TestValidateXerj(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Xerj)
		want   string
	}{
		{"ok", func(x *Xerj) {}, ""},
		{"key to localhost", func(x *Xerj) { x.APIKey = "k" }, ""},
		{"empty url", func(x *Xerj) { x.URL = "  " }, "xerj.url is empty"},
		{"empty index", func(x *Xerj) { x.Index = "" }, "xerj.index is empty"},
		{"not http", func(x *Xerj) { x.URL = "ftp://h" }, "not an http(s) URL"},
		{"key in clear text", func(x *Xerj) {
			x.URL = "http://example.com:9200"
			x.APIKey = "k"
		}, "clear text"},
		{"https key", func(x *Xerj) {
			x.URL = "https://example.com"
			x.APIKey = "k"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Xerj.Enabled = true
			tc.mutate(&c.Xerj)
			err := c.ValidateWithoutKey()
			if !failsAs(err, tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// failsAs reports whether err matches want: no error for "", else one that mentions it.
func failsAs(err error, want string) bool {
	if want == "" {
		return err == nil
	}
	return err != nil && strings.Contains(err.Error(), want)
}
